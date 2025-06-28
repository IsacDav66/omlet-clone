// backend/index.js (Fase 4.5 - Servidor Híbrido HTTP + WebSocket)

const express = require('express');
const http = require('http');
const WebSocket = require('ws');
const cors = require('cors'); // Importamos cors

const PORT = 3000;

// --- Configuración del servidor HTTP con Express ---
const app = express();
app.use(cors()); // Usamos el middleware de CORS para permitir peticiones del dashboard

// Creamos un servidor HTTP a partir de nuestra app de Express
const server = http.createServer(app);

// --- Configuración del servidor de WebSockets ---
// ¡Importante! En lugar de darle un puerto, le pasamos el servidor HTTP
// para que ambos compartan el mismo puerto (3000).
const wss = new WebSocket.Server({ server });

// Usaremos un objeto simple para las salas. La clave es el ID de la sala.
// El valor será un array de clientes (sockets) en esa sala.
const rooms = {};

// --- Nuevo Endpoint HTTP para el Dashboard ---
app.get('/salas', (req, res) => {
    // Vamos a crear una versión "limpia" del objeto de salas para el frontend.
    // No queremos enviar todo el objeto del socket, solo la información relevante.
    const safeRooms = {};
    for (const roomId in rooms) {
        // Para cada sala, solo nos interesa saber cuántos clientes hay.
        // Si la sala está vacía después de que alguien se vaya, no la mostramos.
        if (rooms[roomId].length > 0) {
            safeRooms[roomId] = {
                id: roomId,
                clientCount: rooms[roomId].length,
                // Podríamos añadir más datos aquí si los tuviéramos
            };
        }
    }
    res.json(safeRooms);
});


// --- Lógica del WebSocket (prácticamente sin cambios) ---
wss.on('connection', ws => {
    console.log('[Servidor] Nuevo cliente WebSocket conectado.');

    ws.on('message', message => {
        let data;
        try {
            data = JSON.parse(message);
        } catch (e) {
            console.error('[Servidor] Error: Mensaje inválido recibido:', message);
            return;
        }

        const { event, payload } = data;
        const { room } = payload || {};

        switch (event) {
            case 'join_room':
                if (!rooms[room]) {
                    rooms[room] = [];
                }
                
                rooms[room].push(ws);
                ws.room = room;

                console.log(`[Servidor] Cliente se unió a la sala '${room}'. Total: ${rooms[room].length}`);

                if (rooms[room].length > 1) {
                    const otherClient = rooms[room].find(client => client !== ws && client.readyState === WebSocket.OPEN);
                    if (otherClient) {
                        otherClient.send(JSON.stringify({ event: 'peer_joined' }));
                    }
                }
                break;

            case 'offer':
            case 'answer':
            case 'candidate':
                console.log(`[Servidor] Retransmitiendo '${event}' en la sala '${ws.room}'`);
                const otherClient = rooms[ws.room]?.find(client => client !== ws && client.readyState === WebSocket.OPEN);
                if (otherClient) {
                    otherClient.send(JSON.stringify({ event, payload }));
                }
                break;
        }
    });

    ws.on('close', () => {
        console.log('[Servidor] Cliente WebSocket desconectado.');
        const room = ws.room;
        if (room && rooms[room]) {
            rooms[room] = rooms[room].filter(client => client !== ws);
            console.log(`[Servidor] Cliente eliminado de la sala '${room}'. Quedan: ${rooms[room].length}`);
            
            // Si la sala queda vacía, la eliminamos para no mostrarla en el dashboard
            if (rooms[room].length === 0) {
                console.log(`[Servidor] Sala '${room}' vacía, eliminando.`);
                delete rooms[room];
            } else {
                 // Notificar al par restante que el otro se ha ido
                const remainingClient = rooms[room]?.[0];
                if (remainingClient && remainingClient.readyState === WebSocket.OPEN) {
                    remainingClient.send(JSON.stringify({ event: 'peer_left' }));
                }
            }
        }
    });
});

// --- Iniciar el servidor ---
server.listen(PORT, () => {
    console.log(`✅ Servidor Híbrido (HTTP + WS) iniciado en el puerto ${PORT}`);
});