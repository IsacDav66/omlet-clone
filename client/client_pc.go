// client/client_pc.go
package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/songgao/water"
)

// --- CONFIGURACIÓN ---
const (
	RELAY_IP   = "3.137.193.55" // ¡¡¡REEMPLAZA ESTO!!!
	RELAY_PORT = 5001
	NETMASK    = "255.255.255.0"
	MTU        = 1500
)

func main() {
	if RELAY_IP == "<IP_DE_TU_VPS>" {
		log.Fatal("¡Error! Debes editar el código y poner la IP de tu VPS en la constante RELAY_IP.")
	}

	log.Printf("--- INICIANDO CLIENTE, conectando al Relay en %s ---", RELAY_IP)

	// Crear y configurar la interfaz TAP local
	ifce, err := water.New(water.Config{DeviceType: water.TAP})
	if err != nil {
		log.Fatalf("Error al crear TAP: %v", err)
	}
	defer ifce.Close()

	if len(os.Args) < 2 {
		log.Fatalf("Uso: go run client_pc.go <ip_virtual_para_este_pc>")
	}
	virtualIP := os.Args[1]
	configureTapInterface(ifce.Name(), virtualIP)

	// Conectarse al servidor Relay en el VPS
	relayAddr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", RELAY_IP, RELAY_PORT))
	conn, err := net.DialUDP("udp", nil, relayAddr)
	if err != nil {
		log.Fatalf("Error al conectar con el relay: %v", err)
	}
	defer conn.Close()

	conn.Write([]byte("hola-relay-soy-un-cliente"))
	log.Printf("[UDP] Conexión establecida con el relay.")

	// Goroutines para el reenvío bidireccional
	go func() { // Del Relay (UDP) al TAP
		buf := make([]byte, MTU)
		for {
			n, _ := conn.Read(buf)
			if n > 0 {
				ifce.Write(buf[:n])
			}
		}
	}()
	go func() { // Del TAP al Relay (UDP)
		packet := make([]byte, MTU)
		for {
			n, _ := ifce.Read(packet)
			if n > 0 {
				conn.Write(packet[:n])
			}
		}
	}()

	log.Println("--- Túnel activo vía Relay. ¡A jugar! ---")
	waitForSignal()
}

// --- FUNCIONES DE AYUDA QUE FALTABAN ---

// configureTapInterface usa 'netsh' para asignar la IP en Windows.
func configureTapInterface(ifceName, ipAddress string) {
	log.Printf("Configurando IP %s para la interfaz '%s'...", ipAddress, ifceName)
	cmd := exec.Command("cmd", "/C", fmt.Sprintf("netsh interface ip set address name=\"%s\" static %s %s", ifceName, ipAddress, NETMASK))
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Fatalf("Error al configurar la IP de la interfaz TAP.\nComando ejecutado: %s\nError: %v\nOutput de Windows: %s\n", cmd.String(), err, string(output))
	}
	log.Printf("[TAP] IP asignada correctamente.")
}

// waitForSignal espera por Ctrl+C para cerrar el programa limpiamente.
func waitForSignal() {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	<-sigc
	log.Println("Cerrando...")
	os.Exit(0)
}
