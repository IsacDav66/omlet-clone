// client_pc.go
package main

import (
	"fmt"
	"log"
	"net"
	"os"

	"github.com/songgao/water"
)

// --- CONFIGURACIÓN ---
const (
	RELAY_IP   = "3.137.193.55" // ¡¡¡REEMPLAZA ESTO!!!
	RELAY_PORT = 5001
	VIRTUAL_IP = "10.80.0.1" // Cambia esto para cada cliente (10.80.0.2, .3, etc.)
	NETMASK    = "255.255.255.0"
	MTU        = 1500
)

func main() {
	if RELAY_IP == "3.137.193.55" {
		log.Fatal("¡Error! Debes editar el código y poner la IP de tu VPS en la constante RELAY_IP.")
	}

	log.Printf("--- INICIANDO CLIENTE, conectando al Relay en %s ---", RELAY_IP)

	// 1. Crear y configurar la interfaz TAP local
	ifce, err := water.New(water.Config{DeviceType: water.TAP})
	if err != nil {
		log.Fatalf("Error al crear TAP: %v", err)
	}
	defer ifce.Close()
	// IMPORTANTE: Debes pasar una IP virtual única como argumento al ejecutar
	if len(os.Args) < 2 {
		log.Fatalf("Uso: go run client_pc.go <ip_virtual_para_este_pc>")
	}
	virtualIP := os.Args[1]
	configureTapInterface(ifce.Name(), virtualIP)

	// 2. Conectarse al servidor Relay en el VPS
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
			ifce.Write(buf[:n])
		}
	}()
	go func() { // Del TAP al Relay (UDP)
		packet := make([]byte, MTU)
		for {
			n, _ := ifce.Read(packet)
			conn.Write(packet[:n])
		}
	}()

	log.Println("--- Túnel activo vía Relay. ¡A jugar! ---")
	waitForSignal()
}

// ... (Pega aquí las funciones de ayuda configureTapInterface y waitForSignal) ...
