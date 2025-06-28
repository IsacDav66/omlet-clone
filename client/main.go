// client/main.go
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
	"github.com/songgao/water"
)

// --- CONFIGURACIÓN ---
const (
	SIGNALING_URL = "ws://129.153.131.82:3000" // ¡¡¡Reemplaza si es necesario!!!
	MTU           = 1500
	NETMASK       = "255.255.255.0"
)

// --- Estructuras para los mensajes de señalización ---
type SignalMessage struct {
	Event   string  `json:"event"`
	Payload Payload `json:"payload,omitempty"`
}

type Payload struct {
	Room      string                     `json:"room,omitempty"`
	Target    string                     `json:"target,omitempty"` // A quién va dirigido
	Source    string                     `json:"source,omitempty"` // Quién lo envía
	PeerID    string                     `json:"peerId,omitempty"` // ID de un par que se une/deja
	Peers     []string                   `json:"peers,omitempty"`  // Lista de pares existentes
	SDP       *webrtc.SessionDescription `json:"sdp,omitempty"`
	Candidate *webrtc.ICECandidateInit   `json:"candidate,omitempty"`
}

// --- Estructuras para gestionar las conexiones ---

// Peer representa una conexión P2P con otro cliente
type Peer struct {
	ID             string
	PeerConnection *webrtc.PeerConnection
	DataChannel    *webrtc.DataChannel
}

// --- Variables Globales ---
var (
	tap          *water.Interface
	mode         string // Establecido por main_host.go o main_player.go
	ws           *websocket.Conn
	roomID       string
	selfID       string           // Nuestro propio ID asignado por el servidor (no lo tenemos aún)
	peers        map[string]*Peer // Mapa de todas nuestras conexiones P2P
	peersMutex   sync.RWMutex     // Mutex para proteger el acceso concurrente al mapa de peers
	webrtcConfig webrtc.Configuration
)

func main() {
	if mode == "" {
		log.Fatalf("Modo no especificado. Compilar con -tags 'host' o -tags 'player'.")
	}
	log.Printf("--- INICIANDO CLIENTE EN MODO: %s ---", strings.ToUpper(mode))

	roomID = getRoomIDFromUser()

	// Inicializar mapa de pares y configuración de WebRTC
	peers = make(map[string]*Peer)
	webrtcConfig = webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}

	// Conectar al servidor de señalización
	var err error
	ws, _, err = websocket.DefaultDialer.Dial(SIGNALING_URL, nil)
	if err != nil {
		log.Fatalf("Error al conectar al servidor de señalización: %v", err)
	}
	defer ws.Close()
	log.Println("✅ Conectado al servidor de señalización")

	// Unirse a la sala
	sendSignal(SignalMessage{Event: "join_room", Payload: Payload{Room: roomID}})

	// Iniciar el bucle de escucha de mensajes del WebSocket
	go listenSignalServer()

	// Configurar la interfaz TAP
	var virtualIP string
	if mode == "host" {
		virtualIP = "10.80.0.1"
	} else {
		virtualIP = "10.80.0.2" // Esto necesitará un cambio para N players, pero por ahora vale
	}
	configureAndUpTap(virtualIP)

	// Iniciar el enrutamiento de paquetes desde el TAP (si somos Host)
	if mode == "host" {
		go routePacketsFromTap()
	}

	select {} // Mantener la aplicación corriendo
}

// listenSignalServer maneja todos los mensajes entrantes del servidor WebSocket
func listenSignalServer() {
	for {
		_, message, err := ws.ReadMessage()
		if err != nil {
			log.Printf("Desconectado del servidor de señalización: %v", err)
			os.Exit(1)
		}

		var msg SignalMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("Error al decodificar mensaje: %v", err)
			continue
		}

		switch msg.Event {
		case "existing_peers": // El servidor nos dice quién está ya en la sala
			if mode == "player" {
				log.Printf("[P2P] Hay %d par(es) existentes. El Host nos enviará ofertas.", len(msg.Payload.Peers))
				// El Player esperará pasivamente las ofertas del Host
			}

		case "peer_joined": // Un nuevo par se ha unido
			peerId := msg.Payload.PeerID
			log.Printf("[P2P] Nuevo par se ha unido a la sala: %s", peerId)
			if mode == "host" {
				log.Printf("[P2P] Creando oferta para el nuevo par %s...", peerId)
				createNewPeerConnection(peerId, true) // El Host inicia la oferta
			}

		case "offer":
			if mode == "player" {
				sourceId := msg.Payload.Source
				log.Printf("[P2P] Oferta recibida de %s. Creando respuesta...", sourceId)
				pc := createNewPeerConnection(sourceId, false) // El Player no inicia oferta
				if err := pc.SetRemoteDescription(*msg.Payload.SDP); err != nil {
					log.Printf("Error al establecer descripción remota: %v", err)
					continue
				}
				answer, err := pc.CreateAnswer(nil)
				if err != nil {
					log.Printf("Error al crear respuesta: %v", err)
					continue
				}
				if err := pc.SetLocalDescription(answer); err != nil {
					log.Printf("Error al establecer descripción local: %v", err)
					continue
				}
				sendSignal(SignalMessage{Event: "answer", Payload: Payload{Target: sourceId, SDP: &answer}})
			}

		case "answer":
			sourceId := msg.Payload.Source
			log.Printf("[P2P] Respuesta recibida de %s. Estableciendo conexión...", sourceId)
			peersMutex.RLock()
			peer, ok := peers[sourceId]
			peersMutex.RUnlock()
			if ok {
				if err := peer.PeerConnection.SetRemoteDescription(*msg.Payload.SDP); err != nil {
					log.Printf("Error al establecer descripción remota para %s: %v", sourceId, err)
				}
			}

		case "candidate":
			sourceId := msg.Payload.Source
			peersMutex.RLock()
			peer, ok := peers[sourceId]
			peersMutex.RUnlock()
			if ok {
				if err := peer.PeerConnection.AddICECandidate(*msg.Payload.Candidate); err != nil {
					log.Printf("Error al añadir candidato ICE de %s: %v", sourceId, err)
				}
			}

		case "peer_left":
			peerId := msg.Payload.PeerID
			log.Printf("[P2P] El par %s se ha desconectado.", peerId)
			peersMutex.Lock()
			if peer, ok := peers[peerId]; ok {
				peer.PeerConnection.Close()
				delete(peers, peerId)
			}
			peersMutex.Unlock()
		}
	}
}

// createNewPeerConnection crea una nueva conexión WebRTC para un par específico
func createNewPeerConnection(peerId string, createOffer bool) *webrtc.PeerConnection {
	pc, err := webrtc.NewPeerConnection(webrtcConfig)
	if err != nil {
		log.Fatalf("Error al crear PeerConnection para %s: %v", peerId, err)
	}

	peer := &Peer{
		ID:             peerId,
		PeerConnection: pc,
	}
	peersMutex.Lock()
	peers[peerId] = peer
	peersMutex.Unlock()

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			candidate := c.ToJSON()
			sendSignal(SignalMessage{Event: "candidate", Payload: Payload{Target: peerId, Candidate: &candidate}})
		}
	})

	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		log.Printf("[P2P] Estado de la conexión con %s ha cambiado: %s\n", peerId, s.String())
		if s == webrtc.PeerConnectionStateFailed || s == webrtc.PeerConnectionStateDisconnected {
			log.Printf("La conexión P2P con %s se ha perdido.", peerId)
			// La lógica de 'peer_left' del servidor se encargará de la limpieza
		}
	})

	// El Player espera un canal de datos, el Host lo crea
	if createOffer { // Somos el Host
		dc, err := pc.CreateDataChannel("tunnel", nil)
		if err != nil {
			log.Fatalf("Error al crear DataChannel para %s: %v", peerId, err)
		}
		peer.DataChannel = dc
		handleDataChannel(peer)

		offer, err := pc.CreateOffer(nil)
		if err != nil {
			log.Fatalf("Error al crear oferta para %s: %v", peerId, err)
		}
		pc.SetLocalDescription(offer)
		sendSignal(SignalMessage{Event: "offer", Payload: Payload{Target: peerId, SDP: &offer}})
	} else { // Somos un Player
		pc.OnDataChannel(func(dc *webrtc.DataChannel) {
			log.Printf("[P2P] Nuevo canal de datos '%s' de %s\n", dc.Label(), peerId)
			peersMutex.Lock()
			peers[peerId].DataChannel = dc
			peersMutex.Unlock()
			handleDataChannel(peer)
		})
	}

	return pc
}

// handleDataChannel configura los callbacks para un canal de datos.
// Ahora la lógica de enrutamiento es más compleja.
func handleDataChannel(peer *Peer) {
	peer.DataChannel.OnOpen(func() {
		log.Printf("--- TÚNEL P2P ACTIVO con %s ---", peer.ID)
	})

	peer.DataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		// Paquete recibido de un par
		tap.Write(msg.Data)

		// ¡¡¡LÓGICA CLAVE DEL HOST!!!
		// Si somos el host, reenviamos el paquete a TODOS los OTROS players.
		if mode == "host" {
			peersMutex.RLock()
			for otherPeerId, otherPeer := range peers {
				if otherPeerId != peer.ID && otherPeer.DataChannel != nil && otherPeer.DataChannel.ReadyState() == webrtc.DataChannelStateOpen {
					otherPeer.DataChannel.Send(msg.Data)
				}
			}
			peersMutex.RUnlock()
		}
	})
}

// routePacketsFromTap (SOLO PARA EL HOST) lee del TAP y envía a todos los peers.
func routePacketsFromTap() {
	buf := make([]byte, MTU)
	for {
		n, err := tap.Read(buf)
		if err != nil {
			log.Printf("Error al leer del TAP: %v", err)
			continue
		}
		if n > 0 {
			// Enviar el paquete a todos los peers conectados
			peersMutex.RLock()
			for _, peer := range peers {
				if peer.DataChannel != nil && peer.DataChannel.ReadyState() == webrtc.DataChannelStateOpen {
					peer.DataChannel.Send(buf[:n])
				}
			}
			peersMutex.RUnlock()
		}
	}
}

// --- Funciones de Ayuda ---

// sendSignal es un helper para enviar mensajes JSON al WebSocket de forma segura
func sendSignal(msg SignalMessage) {
	// Añadimos el roomID a todos los mensajes salientes
	msg.Payload.Room = roomID

	bytes, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Error al codificar mensaje: %v", err)
		return
	}
	if err := ws.WriteMessage(websocket.TextMessage, bytes); err != nil {
		log.Printf("Error al enviar mensaje: %v", err)
	}
}

func getRoomIDFromUser() string {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Introduce el nombre de la sala: ")
	roomID, _ := reader.ReadString('\n')
	return strings.TrimSpace(roomID)
}

func configureAndUpTap(ipAddress string) {
	var err error
	tap, err = water.New(water.Config{DeviceType: water.TAP})
	if err != nil {
		log.Fatalf("Error al crear TAP: %v", err)
	}
	log.Printf("Interfaz TAP creada: '%s'", tap.Name())

	log.Printf("Configurando IP %s para la interfaz...", ipAddress)
	if os.Getenv("OS") == "Windows_NT" {
		cmd := exec.Command("netsh", "interface", "ip", "set", "address", "name="+tap.Name(), "source=static", "addr="+ipAddress, "mask="+NETMASK)
		output, err := cmd.CombinedOutput()
		if err != nil {
			log.Fatalf("Error al configurar IP (Windows): %v\n%s", err, string(output))
		}
	} else { // Asumimos Linux/macOS
		cmd := exec.Command("sudo", "ip", "addr", "add", ipAddress+"/24", "dev", tap.Name())
		if err := cmd.Run(); err != nil {
			log.Fatalf("Error al asignar IP: %v", err)
		}
		cmd = exec.Command("sudo", "ip", "link", "set", "dev", tap.Name(), "up")
		if err := cmd.Run(); err != nil {
			log.Fatalf("Error al levantar interfaz: %v", err)
		}
	}
	log.Println("[TAP] Interfaz configurada y activa.")
}
