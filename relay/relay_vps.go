// relay_vps.go
package main

import (
	"fmt"
	"log"
	"net"
)

const (
	RELAY_PORT = 5001
	MTU        = 1500
)

// Mapa para guardar los clientes conectados
var clients = make(map[string]*net.UDPAddr)

func main() {
	log.Println("--- INICIANDO SERVIDOR RELAY EN VPS ---")

	// Escuchar por conexiones UDP en el puerto del relay
	udpAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", RELAY_PORT))
	if err != nil {
		log.Fatalf("Error al resolver dirección: %v", err)
	}

	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		log.Fatalf("Error al escuchar en UDP: %v", err)
	}
	defer conn.Close()
	log.Printf("✅ Relay escuchando en el puerto %d. Esperando clientes...", RELAY_PORT)

	buf := make([]byte, MTU)
	for {
		// Leer un paquete de CUALQUIER cliente
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("Error al leer de UDP: %v", err)
			continue
		}

		clientKey := remoteAddr.String()

		// Si es un cliente nuevo, lo guardamos y anunciamos
		if _, ok := clients[clientKey]; !ok {
			log.Printf("Nuevo cliente conectado: %s", clientKey)
			clients[clientKey] = remoteAddr
		}

		// Reenviar el paquete a TODOS los OTROS clientes
		for key, addr := range clients {
			if key != clientKey {
				//log.Printf("Reenviando %d bytes de %s -> %s", n, clientKey, key) // Descomenta para depurar
				conn.WriteToUDP(buf[:n], addr)
			}
		}
	}
}
