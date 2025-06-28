// backend/index.js (Fase 5 - Multijugador)

const express = require('express');
const http = require('http');
const WebSocket = require('ws');
const cors = require('cors');
const { v4: uuidv4 } = require('uuid'); // Necesitaremos IDs únicos para los clientes

// Primero, instala uuid: npm install uuid
// ----------------------------------------------------

const PORT = 3000;

const app = express();
app.use(cors());
const server = http.createServer(app);
const wss = new WebSocket.Server({ server });

const rooms = {}; // { roomId: { id: roomId, clients: { clientId: ws, ... } } }

app.get('/salas', (req, res) => {
    const safeRooms = {};
    for (const roomId in rooms) {
        const room = rooms[roomId];
        if (Object.keys(room.clients).length > 0) {
            safeRooms[roomId] = {
                id: roomId,
                clientCount: Object.keys(room.clients).length,
                // Podríamos incluso listar los IDs de los clientes si quisiéramos
                clientIds: Object.keys(room.clients)
            };
        }
    }
    res.json(safeRooms);
});

// Función para retransmitir a todos en una sala EXCEPTO a uno
const broadcastToRoom = (roomId, sourceClientId, message) => {
    const room = rooms[roomId];
    if (!room) return;

    for (const clientId in room.clients) {
        if (clientId !== sourceClientId) {
            const client = room.clients[clientId];
            if (client.readyState === WebSocket.OPEN) {
                client.send(JSON.stringify(message));
            }
        }
    }
};

wss.on('connection', ws => {
    // Asignamos un ID único a cada cliente que se conecta
    const clientId = uuidv4();
    ws.id = clientId;
    console.log(`[Servidor] Nuevo cliente conectado: ${clientId}`);

    ws.on('message', message => {
        let data;
        try {
            data = JSON.parse(message);
        } catch (e) { console.error('[Servidor] Mensaje inválido:', message); return; }

        const { event, payload } = data;
        const { room, target, sdp, candidate } = payload || {};
        const roomId = ws.room; // La sala del cliente que envía el mensaje

        switch (event) {
            case 'join_room':
                if (!rooms[room]) {
                    rooms[room] = { id: room, clients: {} };
                }
                
                // Antes de unir al nuevo, obtenemos la lista de los que ya están
                const existingClients = Object.keys(rooms[room].clients);

                // Añadir al nuevo cliente a la sala
                rooms[room].clients[clientId] = ws;
                ws.room = room;

                console.log(`[Servidor] Cliente ${clientId} se unió a la sala '${room}'. Total: ${Object.keys(rooms[room].clients).length}`);

                // 1. Notificar al nuevo cliente sobre los miembros existentes (el Host)
                ws.send(JSON.stringify({
                    event: 'existing_peers',
                    payload: { peers: existingClients }
                }));

                // 2. Notificar a todos los demás que ha llegado un nuevo par
                broadcastToRoom(room, clientId, {
                    event: 'peer_joined',
                    payload: { peerId: clientId }
                });
                break;

            // Para oferta, respuesta y candidato, ahora retransmitimos a un 'target' específico
            case 'offer':
            case 'answer':
            case 'candidate':
                const targetClient = rooms[roomId]?.clients[target];
                if (targetClient && targetClient.readyState === WebSocket.OPEN) {
                    console.log(`[Servidor] Retransmitiendo '${event}' de ${clientId} -> ${target}`);
                    targetClient.send(JSON.stringify({
                        event,
                        payload: { ...payload, source: clientId } // Añadimos quién es el origen
                    }));
                } else {
                    console.warn(`[Servidor] Intento de enviar '${event}' a un target no encontrado: ${target}`);
                }
                break;
        }
    });

    ws.on('close', () => {
        const roomId = ws.room;
        console.log(`[Servidor] Cliente ${clientId} desconectado.`);
        if (roomId && rooms[roomId]) {
            // Eliminar al cliente
            delete rooms[roomId].clients[clientId];
            
            // Si la sala queda vacía, la eliminamos
            if (Object.keys(rooms[roomId].clients).length === 0) {
                console.log(`[Servidor] Sala '${roomId}' vacía, eliminando.`);
                delete rooms[roomId];
            } else {
                // Notificar a todos los demás que este par se ha ido
                broadcastToRoom(roomId, clientId, {
                    event: 'peer_left',
                    payload: { peerId: clientId }
                });
            }
        }
    });
});

server.listen(PORT, () => {
    console.log(`✅ Servidor Multijugador iniciado en el puerto ${PORT}`);
});