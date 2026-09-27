// Seed de identidad: administrador, profesores y estudiantes sintéticos.
//
// Se ejecuta una vez por corrida de medición y su trabajo NO se mide: el login
// verifica bcrypt en el request path y registrar 500 estudiantes cuesta CPU
// real, así que sembrar y medir mezclados falsearía el cuello de botella.
//
// Idempotencia: registrar un usuario que ya existe y está activo devuelve 409,
// y el seed lo tolera cayendo a login. Eso permite reejecutarlo contra la misma
// base sin recrear nada.

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter, Trend } from 'k6/metrics';
import {
  ADMIN_EMAIL, ADMIN_PASS, ADMIN_USER, BASE_URL, ENTORNO, PROFESSOR_COUNT, RUN_ID,
  SEED_PASSWORD, STUDENT_COUNT,
} from './lib/config.js';
import { params, url } from './lib/api.js';
import { corpusReutilizable } from './lib/corpus.js';

// setupTimeout: registrar 500 estudiantes y hacer un login bcrypt tras cada alta
// son ~1000 peticiones en serie, y bcrypt va en el request path contra una VM de
// 2 vCPU. El default de k6 son 60s y no alcanzan.
export const options = { vus: 1, iterations: 1, setupTimeout: '20m' };

// open() solo existe en init, así que la guarda del corpus se evalúa al cargar
// el módulo y no dentro de setup().
const ESTUDIANTES_EXISTEN = corpusReutilizable('estudiantes.json');

const bcryptLogin = new Trend('seed_login_ms');
const registros = new Counter('seed_users_created_total');
const yaExistentes = new Counter('seed_users_existing_total');

// El registro de profesor exige un token de admin: role=student y role=admin
// son públicos, y para el seed solo necesitamos student y professor.
function registro(username, email, role, tokenAdmin) {
  const headers = { 'Content-Type': 'application/json' };
  if (tokenAdmin) headers.Authorization = `Bearer ${tokenAdmin}`;
  const res = http.post(
    url('/api/v1/auth/register'),
    JSON.stringify({ username, email, password: SEED_PASSWORD, role }),
    params({ headers, tags: { op: 'seed_registro' } })
  );
  return res;
}

// El registro no devuelve token, así que cada alta va seguida de un login para
// tener una sesión lista para las corridas medidas.
function login(username) {
  const t0 = Date.now();
  const res = http.post(
    url('/api/v1/auth/login'),
    JSON.stringify({ username, password: SEED_PASSWORD }),
    params({ headers: { 'Content-Type': 'application/json' }, tags: { op: 'seed_login' } })
  );
  bcryptLogin.add(Date.now() - t0, { fase: 'seed' });
  const ok = check(res, {
    'login: 200 con token': (r) => r.status === 200 && !!(r.json() || {}).token,
  });
  if (!ok) {
    throw new Error(`login fallido para ${username}: ${res.status} ${res.body}`);
  }
  return res.json().token;
}

function alta(username, email, role, tokenAdmin) {
  const res = registro(username, email, role, tokenAdmin);
  if (res.status === 201) {
    registros.add(1, { role });
  } else if (res.status === 409) {
    // El usuario ya existe y está activo: se reutiliza, no es un fallo.
    yaExistentes.add(1, { role });
  } else {
    throw new Error(`registro fallido para ${username} (${role}): ${res.status} ${res.body}`);
  }
  return login(username);
}

// Asegura que exista un administrador con el que poder sembrar.
//
// En un despliegue recién creado no hay ninguno: la aplicación no los crea y
// Terraform tampoco puede hacerlo, porque necesitaría la API ya en marcha. El
// registro con role=admin es público en esta aplicación, así que el seed lo usa
// para aprovisionarse el suyo. Sin esto, la suite entera queda bloqueada en un
// entorno nuevo, que es exactamente el caso de GCP.
function asegurarAdmin() {
  try {
    return login(ADMIN_USER);
  } catch (e) {
    // 401 significa que la cuenta no existe (o la contraseña no coincide). En
    // ese caso se registra; cualquier otro fallo se propaga.
    const res = registro(ADMIN_USER, ADMIN_EMAIL, 'admin', null);
    if (res.status !== 201 && res.status !== 409) {
      throw new Error(
        `el administrador ${ADMIN_USER} no existe y no se pudo registrar: ` +
        `${res.status} ${res.body}`
      );
    }
    // Si el registro devuelve 409, la cuenta existe con otra contraseña: el
    // login de abajo volverá a fallar y el mensaje dirá qué revisar.
    try {
      return login(ADMIN_USER);
    } catch (_e) {
      throw new Error(
        `el administrador ${ADMIN_USER} existe pero la contraseña no coincide con ADMIN_PASS. ` +
        'Ajusta ADMIN_PASS o usa otro ADMIN_USER.'
      );
    }
  }
}

export function setup() {
  // Idempotente: si el corpus ya está y es de ESTE entorno, se reutiliza en vez
  // de volver a crear usuarios, que además es caro por el bcrypt del login. Un
  // corpus de otro entorno no se reutiliza: sus tokens apuntan a otra base, y
  // medir contra él produce mil rechazos que parecen defectos de la plataforma.
  if (ESTUDIANTES_EXISTEN) {
    return { reutilizado: true };
  }
  if (!ADMIN_PASS) {
    throw new Error('Falta ADMIN_PASS. El seed no puede autenticarse como administrador.');
  }

  const adminToken = asegurarAdmin();
  const stub = `s${RUN_ID}`;

  // Profesores: el registro con role=professor requiere Bearer de admin.
  const profesores = [];
  for (let i = 0; i < PROFESSOR_COUNT; i += 1) {
    const username = `${stub}-prof${i}`;
    const token = alta(username, `${username}@carga.test`, 'professor', adminToken);
    profesores.push({ indice: i, username, token });
    sleep(0.05);
  }

  // Estudiantes: el registro es público, pero cada uno necesita su propia sesión.
  // bcrypt domina el costo, así que se usa un lote acotado para no saturar la VM
  // y que los timeouts del propio seed no se confundan con un fallo del sistema.
  const CONCURRENCIA = Number(__ENV.SEED_BATCH || 8);
  const estudiantes = [];
  for (let base = 0; base < STUDENT_COUNT; base += CONCURRENCIA) {
    const lote = [];
    const alto = Math.min(base + CONCURRENCIA, STUDENT_COUNT);
    for (let i = base; i < alto; i += 1) {
      lote.push({ indice: i, username: `${stub}-est${i}` });
    }
    const requests = lote.map((e) => ({
      method: 'POST',
      url: url('/api/v1/auth/register'),
      body: JSON.stringify({
        username: e.username,
        email: `${e.username}@carga.test`,
        password: SEED_PASSWORD,
        role: 'student',
      }),
      params: params({
        headers: { 'Content-Type': 'application/json' },
        tags: { op: 'seed_registro' },
      }),
    }));
    const resps = http.batch(requests);
    resps.forEach((r) => {
      if (r.status === 201) registros.add(1, { role: 'student' });
      else if (r.status === 409) yaExistentes.add(1, { role: 'student' });
      else throw new Error(`registro de estudiante ${r.status}: ${r.body}`);
    });
    // Los logins van aparte: van en lote para amortiguar el costo de bcrypt.
    const logins = http.batch(
      lote.map((e) => ({
        method: 'POST',
        url: url('/api/v1/auth/login'),
        body: JSON.stringify({ username: e.username, password: SEED_PASSWORD }),
        params: params({
          headers: { 'Content-Type': 'application/json' },
          tags: { op: 'seed_login' },
        }),
      }))
    );
    logins.forEach((r, i) => {
      const token = (r.json() || {}).token;
      if (!token) throw new Error(`login de estudiante ${lote[i].username}: ${r.status}`);
      estudiantes.push({ indice: lote[i].indice, username: lote[i].username, token });
    });
  }

  return {
    runId: RUN_ID,
    entorno: ENTORNO,
    baseUrl: BASE_URL,
    generadoEn: new Date().toISOString(),
    admin: { username: ADMIN_USER, token: adminToken },
    profesores,
    estudiantes,
    conteos: {
      profesores: PROFESSOR_COUNT,
      estudiantes: STUDENT_COUNT,
      cursos: Number(__ENV.COURSE_COUNT || 20),
    },
  };
}

// Todo el trabajo ocurre en setup(); el default queda vacío.
export default function () {}

// k6 no tiene API de escritura de archivos, así que handleSummary es el único
// punto donde se puede persistir el corpus. setup_data es lo que devolvió
// setup(), y se escribe en un archivo para que las corridas medidas lo lean sin
// tener que volver a sembrar.
export function handleSummary(data) {
  const corpus = data.setup_data || {};
  if (corpus.reutilizado) {
    return { stdout: '\ndatasets/estudiantes.json ya existe: se reutiliza.\n' };
  }
  const creados = (data.metrics.registros_total &&
    data.metrics.registros_total.values &&
    data.metrics.registros_total.values.count) || 0;
  const reutilizados = (data.metrics.seed_users_existing_total &&
    data.metrics.seed_users_existing_total.values &&
    data.metrics.seed_users_existing_total.values.count) || 0;
  // handleSummary se ejecuta incluso cuando setup() falla. Escribir ahí el corpus
  // vacío dejaría un datasets/*.json corrupto que la corrida siguiente leería
  // como válido, así que solo se escribe si el seed realmente produjo datos.
  if (!corpus || Object.keys(corpus).length === 0) {
    return { stdout: '\nAVISO: el seed no produjo datos; no se escribe el corpus.\n' };
  }
  return {
    'datasets/estudiantes.json': JSON.stringify(corpus, null, 2),
    stdout:
      `\nseed identidad: run=${RUN_ID} ` +
      `estudiantes=${corpus.estudiantes ? corpus.estudiantes.length : 0} ` +
      `profesores=${corpus.profesores ? corpus.profesores.length : 0} ` +
      `creados=${creados} reutilizados=${reutilizados}\n` +
      `corpus escrito en datasets/estudiantes.json\n`,
  };
}
