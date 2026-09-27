// Configuración compartida por todos los guiones de k6.
//
// Todo se lee de variables de entorno con valores por defecto pensados para el
// despliegue de la entrega 2. El Taskfile las fija de forma explícita para que
// una corrida sea reproducible solo con ver el `k6 run` que la produjo.
//
// Uso esperado: ejecutar k6 desde el directorio `capacity-planning/`, que es lo
// que hace el Taskfile. Los scripts leen el corpus desde `datasets/` con
// `open()`, que resuelve relativo al archivo del script, y lo escriben con
// `handleSummary()`, que resuelve relativo al directorio actual.

import { sleep } from 'k6';

const env = __ENV;

// Entorno contra el que se corre. No cambia el comportamiento de los guiones,
// pero viaja en cada muestra para que los resultados de local y de GCP no se
// confundan al analizarlos.
export const ENTORNO = env.ENV || 'local';

// BASE_URL apunta al proxy inverso, no al contenedor: es lo que ve el exterior.
// Con ENV=gcp el Taskfile lo resuelve desde `terraform output app_url`.
export const BASE_URL = (env.BASE_URL || 'https://localhost:8080').replace(/\/+$/, '');

// Última red de seguridad contra medir el entorno equivocado. Si se pidió GCP y
// la URL acabó siendo local, casi siempre es que una variable se evaluó antes de
// tiempo o que un `-e BASE_URL=` vacío cayó en el valor por defecto. Medir así
// produce resultados de local presentados como de nube, que es peor que fallar.
if ((env.ENV || 'local') === 'gcp' && /localhost|127\.0\.0\.1/.test(BASE_URL)) {
  throw new Error(
    `ENV=gcp pero BASE_URL apunta a ${BASE_URL}. ` +
    'Revisa que el despliegue esté aplicado (task gcp:url) o exporta BASE_URL explícitamente.'
  );
}

// El certificado de Caddy es interno y autofirmado mientras `domain` esté vacío
// en el despliegue. Con un dominio real hay que poner INSECURE_TLS=false y
// repetir las mediciones.
export const INSECURE_TLS = env.INSECURE_TLS !== 'false';

// Identificador de la corrida. Entra en los slugs de curso, en las claves de
// idempotencia y en las claves de objeto, y por eso debe cambiar en cada
// corrida: la API tiene índice único en courses.slug y en
// attempts.idempotency_key.
export const RUN_ID = (env.RUN_ID || `r${Date.now()}`).toLowerCase().replace(/[^a-z0-9-]/g, '');

// Credenciales del administrador de la seed. Nunca se versionan: vienen de un
// .env local o de variables de entorno.
export const ADMIN_USER = env.ADMIN_USER || 'admin1';
export const ADMIN_PASS = env.ADMIN_PASS || '';
export const ADMIN_EMAIL = env.ADMIN_EMAIL || 'admin1@carga.test';

// Contraseña única para toda la carga sintética. bcrypt corre en el request path
// del login, así que sembrar 500 estudiantes cuesta CPU real: por eso el seed
// es una tarea aparte y no forma parte de las mediciones.
export const SEED_PASSWORD = env.SEED_PASSWORD || env.ADMIN_PASS || 'ClaveSegura123';

// Matrículas por lote en la prematricular del escenario 1. En una WAN, lanzar
// las 2500 de una sola vez hace que el setup pueda agotar su tiempo y aborte la
// corrida entera; trocearlo acota el daño y da un error más claro.
export const PREMATRICULA_LOTE = Number(env.PREMATRICULA_LOTE || 250);

// TUS contra el almacenamiento S3-compatible de GCS es una integración que puede
// no estar resuelta (tusd con UsePathStyle). Se puede desactivar para correr el
// resto del escenario 2 sin que su ausencia se confunda con un fallo.
export const TUS_HABILITADO = env.TUS_HABILITADO !== 'false';

export const THINK_MIN = Number(env.THINK_MIN || 1);
export const THINK_MAX = Number(env.THINK_MAX || 5);

export const STUDENT_COUNT = Number(env.STUDENT_COUNT || 500);
export const COURSE_COUNT = Number(env.COURSE_COUNT || 20);
export const PROFESSOR_COUNT = Number(env.PROFESSOR_COUNT || 4);

// Estructura del curso sembrado. Se declara aquí para que el informe pueda
// citarla y para que el escenario 1 sepa cuántos recursos recorren las
// consultas de detalle y de progreso.
export const COURSE_SHAPE = {
  modules: Number(env.COURSE_MODULES || 3),
  unitsPerModule: Number(env.COURSE_UNITS || 3),
  minRequiredPct: 80,
  resourcesPerUnit: ['text', 'pdf', 'quiz'],
};

// Niveles del escenario 1. L3 se repite al final para comprobar estabilidad,
// como pide el enunciado.
export const ESC1_LEVELS = [
  { id: 'L0', vus: 5, ramp: '30s', hold: '2m' },
  { id: 'L1', vus: 15, ramp: '45s', hold: '3m' },
  { id: 'L2', vus: 30, ramp: '45s', hold: '3m' },
  { id: 'L3', vus: 60, ramp: '1m', hold: '3m' },
  { id: 'L4', vus: 90, ramp: '1m', hold: '3m' },
];

// Escalada por tasa de llegada. Los niveles L0 a L4 miden "cuántos usuarios
// concurrentes aguanta la plataforma" con un bucle cerrado; éstos miden "cuántas
// peticiones por segundo aguanta" con inyección abierta, que es lo que permite
// encontrar el codo del servidor: si la plataforma se degrada, la tasa de llegada
// no se reduce sola. La progresión es geométrica para que el codo caiga dentro de
// la serie en lugar de entre dos niveles.
export const ESC1_ESCALADA = [
  { id: 'T1', rate: 40, hold: '2m' },
  { id: 'T2', rate: 80, hold: '2m' },
  { id: 'T3', rate: 160, hold: '2m' },
  { id: 'T4', rate: 320, hold: '2m' },
  { id: 'T5', rate: 640, hold: '2m' },
];

// Niveles del escenario 2. La concurrencia de los workers se mantiene fija en 10
// durante toda la corrida; lo que sube es el número de VUs que cargan.
export const ESC2_LEVELS = [
  { id: 'M0', vus: 2, ramp: '20s', hold: '4m' },
  { id: 'M1', vus: 4, ramp: '20s', hold: '4m' },
  { id: 'M2', vus: 8, ramp: '20s', hold: '4m' },
];

// Extrae el nivel pedido por LEVEL y devuelve el escenario de k6. Si no se// reconoce, falla ruidosamente en vez de medir algo distinto de lo pedido.
export function escenarioPorNiveles(niveles, etiqueta) {
  const id = env.LEVEL || niveles[0].id;
  const nivel = niveles.find((n) => n.id === id);
  if (!nivel) {
    const validos = niveles.map((n) => n.id).join(', ');
    throw new Error(`LEVEL inválido: "${id}". Niveles disponibles: ${validos}`);
  }
  // Un nivel con `rate` se inyecta como tasa de llegada. Es la diferencia entre
  // preguntar "cuántos usuarios aguanta" y "cuántas peticiones por segundo
  // aguanta": con ramping-vus y una pausa de 1 a 5 segundos el throughput es
  // VUs/(pausa+latencia), así que subir VUs sin bajar la pausa casi no sube la
  // carga y nunca se llega a la rodilla del servidor. La tasa de llegada fija
  // req/s y deja que la latencia crezca, que es lo que revela el límite.
  if (nivel.rate) {
    // El número de VUs lo fija la ley de Little: VUs = tasa x duración del
    // ciclo. Sin pausa el ciclo son unas decenas de milisegundos, así que el
    // tope de VUs queda holgado con el doble de la tasa.
    return {
      executor: 'constant-arrival-rate',
      rate: nivel.rate,
      timeUnit: '1s',
      duration: nivel.hold,
      preAllocatedVUs: Math.max(20, Math.ceil(nivel.rate / 4)),
      maxVUs: nivel.maxVUs || Math.max(100, Math.ceil(nivel.rate * 2)),
      gracefulStop: '30s',
      exec: etiqueta,
      tags: {
        escenario: etiqueta, nivel: nivel.id,
        inyeccion: 'tasa-llegada', tasaObjetivo: String(nivel.rate),
      },
    };
  }
  return {
    executor: 'ramping-vus',
    startVUs: 0,
    stages: [
      { duration: nivel.ramp, target: nivel.vus },
      { duration: nivel.hold, target: nivel.vus },
      { duration: '15s', target: 0 },
    ],
    gracefulRampDown: '15s',
    gracefulStop: '30s',
    exec: etiqueta,
    tags: { escenario: etiqueta, nivel: nivel.id, vusObjetivo: String(nivel.vus) },
  };
}

// Espera de acción simulada. El enunciado pide declarar las pausas entre
// acciones, y sin ellas los VUs serían un open loop artificial.
//
// Con SIN_PAUSA=true la pausa se omite. Sólo se usa en la escalada por tasa de
// llegada, donde la carga la fija el planificador y no el bucle del VU: la pausa
// no cambia la tasa, solo obliga a tener más VUs vivos para sostenerla. Con una
// pausa de 1 a 5 segundos harían falta unos 1 920 VUs para 640 operaciones por
// segundo, que no caben en la VM generadora de 2 GB.
export function think() {
  if (__ENV.SIN_PAUSA === 'true') return;
  const span = Math.max(THINK_MAX - THINK_MIN, 0);
  sleep(THINK_MIN + Math.random() * span);
}
