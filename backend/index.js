// backend/index.js (Fase 4 - Servidor de Señalización con WebSockets)
const WebSocket = require('ws');

const wss = new WebSocket.Server({ port: 3000 });

// Usaremos un objeto simple para las salas. La clave es el ID de la sala.
// El valor será un array de clientes (sockets) en esa sala.
const rooms = {};

console.log('✅ Servidor de Señalización (Fase 4) iniciado en el puerto 3000');

wss.on('connection', ws => {
    console.log('[Servidor] Nuevo cliente conectado.');

    ws.on('message', message => {
        let data;
        try {
            data = JSON.parse(message);
        } catch (e) {
            console.error('[Servidor] Error: Mensaje inválido recibido:', message);
            return;
        }

        const { event, payload } = data;
        const { room, sdp, candidate } = payload || {};

        switch (event) {
            // Un cliente quiere crear o unirse a una sala
            case 'join_room':
                if (!rooms[room]) {
                    rooms[room] = []; // Crear la sala si no existe
                }
                
                // Añadir al cliente a la sala y guardar la sala en el cliente
                rooms[room].push(ws);
                ws.room = room;

                console.log(`[Servidor] Cliente se unió a la sala '${room}'. Total: ${rooms[room].length}`);

                // Si hay más de un cliente, notificamos que un "par" se ha unido
                // para que el primero inicie la oferta P2P.
                if (rooms[room].length > 1) {
                    const otherClient = rooms[room].find(client => client !== ws && client.readyState === WebSocket.OPEN);
                    if (otherClient) {
                        otherClient.send(JSON.stringify({ event: 'peer_joined' }));
                    }
                }
                break;

            // Retransmitir la oferta, respuesta o candidato al OTRO cliente de la sala
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
        console.log('[Servidor] Cliente desconectado.');
        const room = ws.room;
        if (room && rooms[room]) {
            // Eliminar al cliente de la sala
            rooms[room] = rooms[room].filter(client => client !== ws);
            console.log(`[Servidor] Cliente eliminado de la sala '${room}'.`);
            
            // Notificar al otro par que el cliente se ha ido
            const remainingClient = rooms[room]?.[0];
            if (remainingClient && remainingClient.readyState === WebSocket.OPEN) {
                remainingClient.send(JSON.stringify({ event: 'peer_left' }));
            }
        }
    });
});