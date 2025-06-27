// backend/index.js
const express = require('express');
const cors = require('cors');
const morgan = require('morgan');

const app = express();
const PORT = 3000;

// --- Middlewares ---
app.use(cors());
app.use(express.json());
app.use(morgan('dev'));

// --- Base de datos en memoria ---
// Guardará la información de la sala, específicamente la del jugador que espera.
const salas = {};

// --- Utilidades ---
const generarId = () => Math.random().toString(36).substring(2, 9);

// --- Rutas de la API ---

/**
 * RUTA 1: El JUGADOR (en el VPS) crea y anuncia una sala.
 * El jugador envía su propia IP pública (o la del VPS) para que el host pueda encontrarlo.
 */
app.post('/sala/crear', (req, res) => {
    // Para simplificar, generamos el ID de la sala aquí.
    // En una app real, el cliente podría proponer un nombre.
    const salaId = generarId();
    
    // Obtenemos la IP del cliente que hace la petición.
    // 'req.ip' puede dar la IP correcta si el servidor está bien configurado detrás de un proxy.
    // Si no, la enviaremos en el body. Usaremos el body por ahora para ser explícitos.
    const { playerIp, playerPort } = req.body;

    if (!playerIp || !playerPort) {
        return res.status(400).json({ error: 'Faltan playerIp y playerPort' });
    }

    salas[salaId] = {
        id: salaId,
        jugador: {
            ip: playerIp,
            port: playerPort,
        },
        host: null, // El host aún no se ha conectado
        createdAt: new Date(),
    };

    console.log(`[Backend] Sala ${salaId} creada por JUGADOR en ${playerIp}:${playerPort}`);
    res.status(201).json({ salaId: salaId });
});

/**
 * RUTA 2: El HOST (en tu PC) se une a una sala y pide los datos del jugador.
 */
app.post('/sala/unirse', (req, res) => {
    const { salaId } = req.body;
    const sala = salas[salaId];

    if (!sala) {
        return res.status(404).json({ error: 'La sala no existe.' });
    }
    
    if (!sala.jugador) {
        return res.status(404).json({ error: 'La sala está vacía, el jugador no se ha anunciado.' });
    }

    // Guardamos la IP del host que se une (útil para logs o futuras funciones)
    const hostIp = req.ip;
    sala.host = { ip: hostIp };
    
    console.log(`[Backend] HOST desde ${hostIp} se une a la sala ${salaId}`);
    
    // Le devolvemos al host la información del jugador para que se conecte
    res.status(200).json({
        playerIp: sala.jugador.ip,
        playerPort: sala.jugador.port,
    });
});

/**
 * RUTA 3: (Opcional) Para ver el estado de las salas.
 */
app.get('/salas', (req, res) => {
    res.json(salas);
});


app.listen(PORT, '0.0.0.0', () => {
    // Escuchar en '0.0.0.0' es importante para que sea accesible desde fuera del VPS,
    // no solo desde localhost.
    console.log(`✅ Backend (versión VPS) escuchando en el puerto ${PORT}`);
});