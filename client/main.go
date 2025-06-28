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

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
	"github.com/songgao/water"
)

// --- CONFIGURACIÓN ---
const (
	// ¡¡¡IMPORTANTE!!! Reemplaza "localhost" por la IP pública de tu VPS
	SIGNALING_URL = "ws://129.153.131.82:3000"
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

// --- Variables Globales ---
var tap *water.Interface
var mode string // Esta variable será establecida por main_host.go o main_player.go

// --- Función Principal ---
func main() {
	if mode == "" {
		log.Fatalf("Modo no especificado. Debes compilar con -tags 'host' o -tags 'player'.")
	}
	if SIGNALING_URL == "ws://<IP_VPS_ORACLE>:3000" {
		log.Fatal("¡Error! Debes editar main.go y poner la IP de tu VPS en la constante SIGNALING_URL.")
	}

	log.Printf("--- INICIANDO CLIENTE EN MODO: %s ---", strings.ToUpper(mode))

	roomID := getRoomIDFromUser()

	// Conectar al servidor de señalización
	ws, _, err := websocket.DefaultDialer.Dial(SIGNALING_URL, nil)
	if err != nil {
		log.Fatalf("Error al conectar al servidor de señalización: %v", err)
	}
	defer ws.Close()
	log.Println("✅ Conectado al servidor de señalización")

	ws.WriteJSON(SignalMessage{Event: "join_room", Payload: Payload{Room: roomID}})

	peerConnection, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	})
	if err != nil {
		log.Fatalf("Error al crear PeerConnection: %v", err)
	}

	peerConnection.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		candidate := c.ToJSON()
		ws.WriteJSON(SignalMessage{Event: "candidate", Payload: Payload{Room: roomID, Candidate: &candidate}})
	})

	peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		log.Printf("[P2P] Estado de la conexión ha cambiado: %s\n", s.String())
		if s == webrtc.PeerConnectionStateFailed || s == webrtc.PeerConnectionStateDisconnected {
			log.Println("La conexión P2P se ha perdido.")
		}
	})

	peerConnection.OnDataChannel(func(d *webrtc.DataChannel) {
		log.Printf("[P2P] Nuevo canal de datos '%s' - '%d'\n", d.Label(), d.ID())
		d.OnOpen(func() {
			handleDataChannel(d)
		})
	})

	go func() {
		for {
			_, message, err := ws.ReadMessage()
			if err != nil {
				log.Printf("Desconectado del servidor de señalización: %v", err)
				os.Exit(1)
			}

			var msg SignalMessage
			json.Unmarshal(message, &msg)

			switch msg.Event {
			case "peer_joined":
				if mode == "host" {
					log.Println("[P2P] Un par se ha unido. Creando oferta...")
					dataChannel, _ := peerConnection.CreateDataChannel("tunnel", nil)
					handleDataChannel(dataChannel)
					offer, _ := peerConnection.CreateOffer(nil)
					peerConnection.SetLocalDescription(offer)
					ws.WriteJSON(SignalMessage{Event: "offer", Payload: Payload{Room: roomID, SDP: &offer}})
				}
			case "offer":
				if mode == "player" {
					log.Println("[P2P] Oferta recibida. Creando respuesta...")
					payload := msg.Payload.(map[string]interface{})
					sdpMap := payload["sdp"].(map[string]interface{})
					var sdp webrtc.SessionDescription
					sdp.Type = webrtc.NewSDPType(sdpMap["type"].(string))
					sdp.SDP = sdpMap["sdp"].(string)

					peerConnection.SetRemoteDescription(sdp)
					answer, _ := peerConnection.CreateAnswer(nil)
					peerConnection.SetLocalDescription(answer)
					ws.WriteJSON(SignalMessage{Event: "answer", Payload: Payload{Room: roomID, SDP: &answer}})
				}
			case "answer":
				if mode == "host" {
					log.Println("[P2P] Respuesta recibida. Estableciendo conexión...")
					payload := msg.Payload.(map[string]interface{})
					sdpMap := payload["sdp"].(map[string]interface{})
					var sdp webrtc.SessionDescription
					sdp.Type = webrtc.NewSDPType(sdpMap["type"].(string))
					sdp.SDP = sdpMap["sdp"].(string)
					peerConnection.SetRemoteDescription(sdp)
				}
			case "candidate":
				payload := msg.Payload.(map[string]interface{})
				candidateMap := payload["candidate"].(map[string]interface{})
				var candidate webrtc.ICECandidateInit
				candidate.Candidate = candidateMap["candidate"].(string)
				sdpMLineIndex := uint16(candidateMap["sdpMLineIndex"].(float64))
				candidate.SDPMLineIndex = &sdpMLineIndex
				peerConnection.AddICECandidate(candidate)
			}
		}
	}()

	var virtualIP string
	if mode == "host" {
		virtualIP = "10.80.0.1"
	} else {
		virtualIP = "10.80.0.2"
	}
	configureAndUpTap(virtualIP)

	select {}
}

// handleDataChannel conecta el TAP al canal P2P
func handleDataChannel(dc *webrtc.DataChannel) {
	log.Println("--- ¡TÚNEL P2P ACTIVO! ---")
	go func() {
		buf := make([]byte, MTU)
		for {
			n, _ := tap.Read(buf)
			if n > 0 {
				dc.Send(buf[:n])
			}
		}
	}()
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		tap.Write(msg.Data)
	})
}

// --- Funciones de Ayuda ---

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

	log.Printf("Configurando IP %s para la interfaz '%s'...", ipAddress, tap.Name())
	if os.Getenv("OS") == "Windows_NT" {
		cmd := exec.Command("cmd", "/C", fmt.Sprintf("netsh interface ip set address name=\"%s\" static %s %s", tap.Name(), ipAddress, NETMASK))
		if err := cmd.Run(); err != nil {
			log.Fatalf("Error al configurar IP (Windows): %v", err)
		}
	} else { // Asumimos Linux
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
