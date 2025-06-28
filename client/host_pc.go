// client/host_pc.go
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

	"github.com/songgao/water"
)

// --- CONFIGURACIÓN ---
const (
	// ¡¡¡IMPORTANTE!!! Reemplaza "localhost" por la IP pública de tu VPS
	BACKEND_URL = "http://3.137.193.55:3000"

	LISTEN_PORT     = 5001 // Puerto UDP que ABRIRÁS en tu router para el jugador
	HOST_VIRTUAL_IP = "10.80.0.1"
	NETMASK         = "255.255.255.0"
	MTU             = 1500
)

// --- Estructuras para la API ---
type CreateRoomResponse struct {
	SalaId string `json:"salaId"`
}

// Mapa para guardar los jugadores conectados
var players = make(map[string]*net.UDPAddr)

// --- Función Principal ---
func main() {
	log.Println("--- INICIANDO HOST EN PC LOCAL ---")

	// 1. Anunciarse en el backend para crear la sala
	salaId := createRoomInBackend()
	log.Printf("✅ Sala creada con ID: %s. El jugador del VPS deberá usar este ID.", salaId)
	log.Println("Asegúrate de haber abierto el puerto UDP", LISTEN_PORT, "en tu router (Port Forwarding).")

	// 2. Crear y configurar la interfaz TAP local
	ifce, err := water.New(water.Config{DeviceType: water.TAP})
	if err != nil {
		log.Fatalf("Error al crear TAP: %v. Asegúrate de haber instalado el driver correcto y ejecutar como Administrador.", err)
	}
	defer ifce.Close()
	configureTapInterface(ifce.Name(), HOST_VIRTUAL_IP)

	// 3. Escuchar por conexiones UDP del jugador
	udpAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", LISTEN_PORT))
	if err != nil {
		log.Fatalf("Error al resolver dirección UDP: %v", err)
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		log.Fatalf("Error al escuchar en UDP: %v", err)
	}
	defer conn.Close()
	log.Printf("[UDP] Escuchando conexiones de jugadores en el puerto %d...", LISTEN_PORT)

	// Goroutine para reenviar paquetes del TAP al Jugador(es)
	go func() {
		packet := make([]byte, MTU)
		for {
			n, err := ifce.Read(packet)
			if err != nil {
				log.Printf("Error al leer de TAP: %v", err)
				continue
			}

			// Reenviar a todos los jugadores conectados
			for _, playerAddr := range players {
				_, err := conn.WriteToUDP(packet[:n], playerAddr)
				if err != nil {
					log.Printf("Error al enviar paquete a %s: %v", playerAddr.String(), err)
				}
			}
		}
	}()

	log.Println("--- Host listo. Ahora, abre un juego y ponlo en modo LAN. ---")

	// Bucle para recibir del Jugador y reenviar al TAP
	// Bucle para recibir del Jugador y reenviar al TAP
	buf := make([]byte, MTU)
	var playerAddr *net.UDPAddr // Guardaremos la dirección del único jugador aquí

	for {
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("Error al leer de UDP: %v", err)
			continue
		}

		// Si es el primer paquete, guardamos la dirección del jugador
		if playerAddr == nil {
			log.Printf("[UDP] ¡Jugador conectado desde %s!", remoteAddr.String())
			playerAddr = remoteAddr

			// Iniciar una goroutine para leer del TAP y enviar al jugador
			// La iniciamos AQUÍ para asegurarnos de que ya conocemos al jugador
			go func() {
				packet := make([]byte, MTU)
				for {
					n, err := ifce.Read(packet)
					if err != nil {
						log.Printf("Error al leer de TAP: %v", err)
						continue
					}
					// Usamos la misma conexión para enviar de vuelta
					conn.WriteToUDP(packet[:n], playerAddr)
				}
			}()
		}

		// Inyectar paquete en la interfaz TAP
		ifce.Write(buf[:n])
	}
}

// --- Funciones de Ayuda ---

func createRoomInBackend() string {
	// IMPORTANTE: Para que esto funcione, necesitas tu IP PÚBLICA.
	// Esta función intenta obtenerla automáticamente, pero si falla, deberás ponerla a mano.
	myPublicIp, err := getPublicIp()
	if err != nil {
		log.Fatalf("Error crítico: No se pudo obtener la IP pública. %v. Edita el código y ponla manualmente.", err)
	}
	log.Printf("IP pública detectada: %s", myPublicIp)

	reqBody, _ := json.Marshal(map[string]interface{}{"playerIp": myPublicIp, "playerPort": LISTEN_PORT})
	resp, err := http.Post(fmt.Sprintf("%s/sala/crear", BACKEND_URL), "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		log.Fatalf("Error al crear sala en el backend: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		log.Fatalf("El backend devolvió un estado no esperado: %s", resp.Status)
	}

	var apiResp CreateRoomResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		log.Fatalf("Error al decodificar la respuesta del backend: %v", err)
	}
	return apiResp.SalaId
}

func getPublicIp() (string, error) {
	// Usamos un servicio externo para saber nuestra IP pública
	resp, err := http.Get("https://api.ipify.org")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	ip, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(ip), nil
}

func configureTapInterface(ifceName, ipAddress string) {
	log.Printf("Configurando IP %s para la interfaz '%s'...", ipAddress, ifceName)
	cmd := exec.Command("cmd", "/C", fmt.Sprintf("netsh interface ip set address name=\"%s\" static %s %s", ifceName, ipAddress, NETMASK))
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Fatalf("Error al configurar la IP de la interfaz TAP.\nComando ejecutado: %s\nError: %v\nOutput de Windows: %s\n", cmd.String(), err, string(output))
	}
	log.Printf("[TAP] IP asignada correctamente.")
}

func waitForSignal() {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	<-sigc
	log.Println("Cerrando...")
	os.Exit(0)
}
