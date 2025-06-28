// client/p2p_client.go
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
	"github.com/songgao/water"
)

// --- CONFIGURACIÓN ---
const (
	// ¡¡¡IMPORTANTE!!! Reemplaza "localhost" por la IP pública de tu VPS
	SIGNALING_URL = "ws://3.137.193.55:3000"
	MTU           = 1500
	NETMASK       = "255.255.255.0"
)

// --- Estructuras para los mensajes de señalización ---
type SignalMessage struct {
	Event   string      `json:"event"`
	Payload interface{} `json:"payload,omitempty"`
}

type Payload struct {
	Room      string                     `json:"room,omitempty"`
	SDP       *webrtc.SessionDescription `json:"sdp,omitempty"`
	Candidate *webrtc.ICECandidateInit   `json:"candidate,omitempty"`
}

var tap *water.Interface

func main() {
	// Determinar el modo (host o player) y la sala
	mode, roomID := getModeAndRoom()

	// Conectar al servidor de señalización
	ws, _, err := websocket.DefaultDialer.Dial(SIGNALING_URL, nil)
	if err != nil {
		log.Fatalf("Error al conectar al servidor de señalización: %v", err)
	}
	defer ws.Close()
	log.Println("✅ Conectado al servidor de señalización")

	// Unirse a la sala
	ws.WriteJSON(SignalMessage{Event: "join_room", Payload: Payload{Room: roomID}})

	// Configuración de WebRTC (usando servidores STUN públicos de Google)
	peerConnection, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	})
	if err != nil {
		log.Fatalf("Error al crear PeerConnection: %v", err)
	}

	// Manejar candidatos ICE (el núcleo del Hole Punching)
	peerConnection.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		candidate := c.ToJSON()
		ws.WriteJSON(SignalMessage{Event: "candidate", Payload: Payload{Room: roomID, Candidate: &candidate}})
	})

	// Cuando la conexión P2P se establece
	peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		log.Printf("[P2P] Estado de la conexión ha cambiado: %s\n", s.String())
		if s == webrtc.PeerConnectionStateFailed {
			log.Println("La conexión P2P ha fallado.")
			os.Exit(1)
		}
	})

	// El canal de datos es nuestro nuevo túnel
	peerConnection.OnDataChannel(func(d *webrtc.DataChannel) {
		log.Printf("[P2P] Nuevo canal de datos '%s' - '%d'\n", d.Label(), d.ID())
		d.OnOpen(func() {
			handleDataChannel(d)
		})
	})

	// Bucle para leer mensajes del servidor de señalización
	go func() {
		for {
			_, message, err := ws.ReadMessage()
			if err != nil {
				log.Printf("Error al leer del WebSocket: %v", err)
				return
			}

			var msg SignalMessage
			json.Unmarshal(message, &msg)

			switch msg.Event {
			case "peer_joined":
				// Soy el host, un jugador se unió. Le envío una oferta.
				log.Println("[P2P] Un par se ha unido. Creando oferta...")
				dataChannel, _ := peerConnection.CreateDataChannel("tunnel", nil)
				handleDataChannel(dataChannel)
				offer, _ := peerConnection.CreateOffer(nil)
				peerConnection.SetLocalDescription(offer)
				ws.WriteJSON(SignalMessage{Event: "offer", Payload: Payload{Room: roomID, SDP: &offer}})

			case "offer":
				// Soy el jugador, recibí una oferta. Respondo.
				log.Println("[P2P] Oferta recibida. Creando respuesta...")
				var payload Payload
				json.Unmarshal([]byte(message), &SignalMessage{Payload: &payload})
				peerConnection.SetRemoteDescription(*payload.SDP)
				answer, _ := peerConnection.CreateAnswer(nil)
				peerConnection.SetLocalDescription(answer)
				ws.WriteJSON(SignalMessage{Event: "answer", Payload: Payload{Room: roomID, SDP: &answer}})

			case "answer":
				// Soy el host, recibí la respuesta.
				log.Println("[P2P] Respuesta recibida. Estableciendo conexión...")
				var payload Payload
				json.Unmarshal([]byte(message), &SignalMessage{Payload: &payload})
				peerConnection.SetRemoteDescription(*payload.SDP)

			case "candidate":
				// Ambos reciben candidatos del otro.
				var payload Payload
				json.Unmarshal([]byte(message), &SignalMessage{Payload: &payload})
				peerConnection.AddICECandidate(*payload.Candidate)
			}
		}
	}()

	// Crear y configurar la interfaz TAP
	var virtualIP string
	if mode == "host" {
		virtualIP = "10.80.0.1"
	} else {
		virtualIP = "10.80.0.2"
	}
	configureAndUpTap(virtualIP)

	select {} // Bloquear para mantener el programa corriendo
}

// handleDataChannel es donde la magia ocurre: conectar el TAP al canal P2P
func handleDataChannel(dc *webrtc.DataChannel) {
	log.Println("--- ¡TÚNEL P2P ACTIVO! ---")
	// Leer del TAP y enviar al canal de datos
	go func() {
		buf := make([]byte, MTU)
		for {
			n, _ := tap.Read(buf)
			if n > 0 {
				dc.Send(buf[:n])
			}
		}
	}()
	// Leer del canal de datos y escribir en el TAP
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		tap.Write(msg.Data)
	})
}

// --- Funciones de Ayuda ---

func getModeAndRoom() (string, string) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("¿Quieres ser 'host' o 'player'? ")
	mode, _ := reader.ReadString('\n')
	mode = strings.TrimSpace(mode)

	if mode != "host" && mode != "player" {
		log.Fatalf("Modo inválido.")
	}

	fmt.Print("Introduce el nombre de la sala: ")
	roomID, _ := reader.ReadString('\n')
	roomID = strings.TrimSpace(roomID)
	return mode, roomID
}

func configureAndUpTap(ipAddress string) {
	var err error
	tap, err = water.New(water.Config{DeviceType: water.TAP})
	if err != nil {
		log.Fatalf("Error al crear TAP: %v", err)
	}

	log.Printf("Configurando IP %s para la interfaz '%s'...", ipAddress, tap.Name())
	if os.Getenv("OS") == "Windows_NT" {
		cmd := exec.Command("cmd", "/C", fmt.Sprintf("netsh interface ip set address name=\"%s\" static %s %s", tap.Name(), ipAddress, NETMASK))
		if err := cmd.Run(); err != nil {
			log.Fatalf("Error al configurar IP (Windows): %v", err)
		}
	} else { // Asumimos Linux/macOS
		cmd := exec.Command("ip", "addr", "add", ipAddress+"/24", "dev", tap.Name())
		if err := cmd.Run(); err != nil {
			log.Fatalf("Error al asignar IP: %v", err)
		}
		cmd = exec.Command("ip", "link", "set", "dev", tap.Name(), "up")
		if err := cmd.Run(); err != nil {
			log.Fatalf("Error al levantar interfaz: %v", err)
		}
	}
	log.Println("[TAP] Interfaz configurada y activa.")
}
