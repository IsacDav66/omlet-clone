// client/player_vps.go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/songgao/water"
)

// --- CONFIGURACIÓN ---
const (
	// ¡¡¡IMPORTANTE!!! Reemplaza "localhost" por la IP pública de tu VPS
	BACKEND_URL = "http://3.137.193.55:3000"

	PLAYER_VIRTUAL_IP = "10.80.0.2" // IP virtual para este jugador
	NETMASK           = "255.255.255.0"
	MTU               = 1500
)

// --- Estructuras para la API ---
type JoinRoomResponse struct {
	PlayerIp   string `json:"playerIp"`   // El backend devuelve la IP del host aquí
	PlayerPort int    `json:"playerPort"` // El puerto del host
}

// --- Función Principal ---
func main() {
	log.Println("--- INICIANDO JUGADOR EN VPS ---")

	if len(os.Args) < 2 {
		log.Fatalf("Error: Debes proporcionar un ID de sala. Uso: go run player_vps.go <salaId>")
	}
	salaId := os.Args[1]

	// 1. Unirse a la sala para obtener la IP del host (que está en tu PC local)
	hostIp, hostPort := joinRoomInBackend(salaId)
	hostAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", hostIp, hostPort))
	if err != nil {
		log.Fatalf("Error al resolver la dirección del host: %v", err)
	}
	log.Printf("[Backend] Conectando al host en %s...", hostAddr.String())

	// 2. Crear y configurar la interfaz TAP en el VPS
	ifce, err := water.New(water.Config{DeviceType: water.TAP})
	if err != nil {
		log.Fatalf("Error al crear TAP: %v", err)
	}
	defer ifce.Close()
	configureTapInterface(ifce.Name(), PLAYER_VIRTUAL_IP)

	// 3. Conectarse al host en tu PC a través de internet
	conn, err := net.DialUDP("udp", nil, hostAddr)
	if err != nil {
		log.Fatalf("Error al conectar con el host: %v. ¿El host está corriendo y el puerto está abierto (Port Forwarding)?", err)
	}
	defer conn.Close()

	// Enviar un paquete inicial para "presentarse"
	conn.Write([]byte("hola-host-soy-el-jugador-del-vps"))
	log.Printf("[UDP] Conexión establecida con el host en %s", hostAddr.String())

	// *** AÑADIR ESTA GOROUTINE ***
	// Enviar un "keep-alive" cada 10 segundos para mantener el agujero NAT abierto
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			conn.Write([]byte("keep-alive"))
		}
	}()

	// Goroutine para leer del host (UDP) y escribir en la red virtual (TAP)
	go func() {
		buf := make([]byte, MTU)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				log.Printf("Conexión con el host perdida: %v", err)
				os.Exit(1)
			}
			_, err = ifce.Write(buf[:n])
			if err != nil {
				log.Printf("Error al escribir en TAP: %v", err)
			}
		}
	}()

	// Goroutine para leer de la red virtual (TAP) y enviar al host (UDP)
	go func() {
		packet := make([]byte, MTU)
		for {
			n, err := ifce.Read(packet)
			if err != nil {
				log.Printf("Error al leer de TAP: %v", err)
				continue
			}
			_, err = conn.Write(packet[:n])
			if err != nil {
				log.Printf("Error al enviar paquete al host: %v", err)
			}
		}
	}()

	log.Println("--- Túnel activo. Esperando tráfico del juego. ---")
	waitForSignal()
}

// --- Funciones de Ayuda ---

func joinRoomInBackend(salaId string) (string, int) {
	reqBody, _ := json.Marshal(map[string]string{"salaId": salaId})
	// AÑADIMOS "/sala" a la URL
	resp, err := http.Post(fmt.Sprintf("%s/sala/unirse", BACKEND_URL), "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		log.Fatalf("Error crítico al unirse a la sala en el backend: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Este log es más detallado y nos ayudará si hay más problemas
		body, _ := io.ReadAll(resp.Body)
		log.Fatalf("El backend devolvió un estado no esperado al unirse: %s. Respuesta: %s", resp.Status, string(body))
	}

	var apiResp JoinRoomResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		log.Fatalf("Error al decodificar la respuesta del backend: %v", err)
	}
	return apiResp.PlayerIp, apiResp.PlayerPort
}

func configureTapInterface(ifceName, ipAddress string) {
	// ¡¡¡IMPORTANTE!!! Este comando es para Windows. Si tu VPS es Linux, debes cambiarlo.
	// Comenta la línea de 'cmd' y descomenta las de 'ip'.
	log.Printf("Configurando IP %s para la interfaz '%s'...", ipAddress, ifceName)

	// --- Para Windows Server ---
	cmd := exec.Command("cmd", "/C", fmt.Sprintf("netsh interface ip set address name=\"%s\" static %s %s", ifceName, ipAddress, NETMASK))
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Fatalf("Error al configurar la IP (Windows).\nComando: %s\nError: %v\nOutput: %s\n", cmd.String(), err, string(output))
	}

	/*
		// --- Para Linux (descomenta estas líneas y comenta las de Windows si tu VPS es Linux) ---
		// 1. Asignar la IP
		cmdIp := exec.Command("ip", "addr", "add", fmt.Sprintf("%s/%s", ipAddress, "24"), "dev", ifceName)
		if err := cmdIp.Run(); err != nil {
			log.Fatalf("Error al asignar IP (Linux): %v", err)
		}
		// 2. Levantar la interfaz
		cmdUp := exec.Command("ip", "link", "set", "dev", ifceName, "up")
		if err := cmdUp.Run(); err != nil {
			log.Fatalf("Error al levantar la interfaz (Linux): %v", err)
		}
	*/

	log.Printf("[TAP] IP asignada correctamente.")
}

func waitForSignal() {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	<-sigc
	log.Println("Cerrando...")
	os.Exit(0)
}
