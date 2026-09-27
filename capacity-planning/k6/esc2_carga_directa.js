// Escenario 2a. Carga directa al almacenamiento de objetos.
//
// Simula profesores que suben archivos con URL prefirmada. Los bytes NO pasan por
// la API: van del cliente al almacenamiento. Eso obliga a separar el tráfico en
// dos, que es lo que pide el enunciado:
//   - control: el POST que autoriza la carga y devuelve la URL prefirmada
//   - transferencia: el PUT al almacenamiento, que no toca la API
//
// El guion corre con pocos VU a propósito. Cada VU sostiene en memoria el archivo
// completo (hasta ~22 MB en p3-large), así que un nivel alto ahogaría al propio
// generador antes que a la plataforma. La tasa de carga se sube con niveles, no
// con volumen.
//
// La clave del objeto incluye el milisegundo de encolado para dos razones:
//   1. asynq deduplica por (resource_id, object_key) con TTL de 24h, así que
//      repetir la clave descartaría el trabajo en silencio y task_id quedaría
//      vacío. Cambiarla por iteración es obligatorio.
//   2. ese milisegundo es lo que permite medir time-to-available sin estado
//      compartido entre VU: el drenaje parsea la misma clave.

import http from 'k6/http';
import exec from 'k6/execution';
import { sleep } from 'k6';
import { Counter, Trend } from 'k6/metrics';
import { ESC2_LEVELS, RUN_ID, escenarioPorNiveles, think } from './lib/config.js';
import {
  OPS, body, json, latenciaControl, latenciaTransferencia, params,
  throughputTransferencia, url,
} from './lib/api.js';
import { validaAutorizaCarga } from './lib/checks.js';
import { abrirDataset, abrirMedio } from './lib/corpus.js';
import { bucleObservacion, escenarioObservacion } from './lib/observacion.js';

const IDENTIDAD = abrirDataset('estudiantes.json');
const CONTENIDO = abrirDataset('cursos.json');

export const options = {
  scenarios: {
    carga: escenarioPorNiveles(ESC2_LEVELS, 'cargaDirecta'),
    observacion: escenarioObservacion(),
  },
  thresholds: {
    functional_error_rate: ['rate<0.01'],
  },
  summaryTrendStats: ['p(50)', 'p(90)', 'p(95)', 'p(99)', 'max', 'min', 'avg'],
};

const PERFILES = [
  { clave: 'p1-small', archivo: 'p1-small.mp4', mime: 'video/mp4', tipo: 'video', extension: 'mp4' },
  { clave: 'p2-medium', archivo: 'p2-medium.mp4', mime: 'video/mp4', tipo: 'video', extension: 'mp4' },
  { clave: 'p3-large', archivo: 'p3-large.mp4', mime: 'video/mp4', tipo: 'video', extension: 'mp4' },
  { clave: 'a1-audio', archivo: 'a1-audio.m4a', mime: 'audio/mp4', tipo: 'audio', extension: 'm4a' },
];

const cargaAceptada = new Counter('upload_accepted_total');
const taskIdVacio = new Counter('upload_task_id_empty_total');
const transferenciaFallida = new Counter('upload_transfer_failed_total');
const latenciaCrear = new Trend('ctrl_create_resource_ms');
const latenciaConfirmar = new Trend('ctrl_confirm_ms');
const hastaConfirmacion = new Trend('xfer_hasta_confirmacion_ms');

// open() solo funciona en contexto de init. Los archivos se cargan una vez por
// proceso de VU; con 2 a 8 VU la memoria es acotada.
const CUERPOS = {};
for (const p of PERFILES) {
  CUERPOS[p.clave] = abrirMedio(p.archivo);
}

export function setup() {
  const borradores = CONTENIDO.borradores || [];
  if (!borradores.length) {
    throw new Error('datasets/cursos.json no tiene borradores. Corre antes: task seed:courses');
  }
  const profesores = {};
  for (const b of borradores) {
    const prof = IDENTIDAD.profesores.find((x) => x.indice === b.profesorIndice);
    if (prof) profesores[b.profesorIndice] = prof;
  }
  return {
    adminToken: IDENTIDAD.admin.token,
    runId: RUN_ID,
    borradores,
    profesores,
    perfiles: PERFILES,
  };
}

export function cargaDirecta(data) {
  const perfil = data.perfiles[exec.vu.iterationInInstance % data.perfiles.length];
  const bytes = CUERPOS[perfil.clave];
  const borrador = data.borradores[exec.vu.iterationInInstance % data.borradores.length];
  const prof = data.profesores[borrador.profesorIndice];
  if (!prof) return;

  // 1. Crear el recurso en el borrador. AddResource exige versión editable, por
  //    eso los borradores existen en el corpus.
  const t0 = Date.now();
  const resR = json('POST', `/api/v1/units/${borrador.unidadId}/resources`,
    { type: perfil.tipo, title: `${perfil.clave} ${data.runId} v${exec.vu.iterationInInstance}` },
    prof.token, 'crea_recurso');
  if (resR.status !== 201) return;
  // La respuesta viene envuelta: {"resource": {...}}. Leer el envelope entero
  // dejaba recurso.id en undefined y la ruta del upload salía como
  // /resources/undefined/upload-url.
  const recurso = (body(resR) || {}).resource;
  if (!recurso || !recurso.id) {
    throw new Error(`crear recurso devolvio una respuesta inesperada: ${resR.body}`);
  }
  latenciaCrear.add(Date.now() - t0);

  // 2. Autorizar la carga: traffic de control contra la API.
  const t1 = Date.now();
  const encoladoEnMs = Date.now();
  const objectKey =
    `k6/${data.runId}/${perfil.clave}-${exec.vu.idInInstance}-${exec.vu.iterationInInstance}-${encoladoEnMs}.${perfil.extension}`;
  const resU = json('POST', `/api/v1/resources/${recurso.id}/upload-url`,
    { object_key: objectKey, mime_type: perfil.mime, size_bytes: bytes.byteLength },
    prof.token, OPS.authCarga);
  if (resU.status !== 200) return;
  const autorizado = body(resU);
  if (!validaAutorizaCarga(autorizado)) {
    // Sin task_id el trabajo nunca se procesa: es una carga perdida y hay que
    // contarla, no hidingla.
    if (!autorizado || !autorizado.task_id) taskIdVacio.add(1);
    return;
  }
  latenciaControl.add(Date.now() - t1, { op: OPS.authCarga });

  // 3. Transferencia directa al almacenamiento. No pasa por la API.
  const t2 = Date.now();
  const resP = http.put(autorizado.upload_url, bytes,
    params({ tags: { op: OPS.transferencia, perfil: perfil.clave } }));
  latenciaTransferencia.add(resP.timings.duration, { perfil: perfil.clave });
  throughputTransferencia.add(bytes.byteLength, { perfil: perfil.clave });
  if (resP.status < 200 || resP.status >= 300) {
    transferenciaFallida.add(1, { perfil: perfil.clave, status: String(resP.status) });
    return;
  }

  // 4. Confirmación: releer el recurso para ver que quedó en processing. No hay
  //    endpoint de "carga completada": el estado vive en el recurso, y eso es
  //    justamente lo que después lee el drenaje.
  const t3 = Date.now();
  const resC = http.get(
    url(`/api/v1/courses/${borrador.cursoId}`),
    params({
      headers: { Authorization: `Bearer ${prof.token}` },
      tags: { op: 'confirma_carga' },
    })
  );
  const r = recursoEnArbol(resC.json() || {}, recurso.id);
  latenciaConfirmar.add(Date.now() - t3);
  latenciaControl.add(Date.now() - t0, { op: 'recorrido_carga' });
  hastaConfirmacion.add(Date.now() - t2, { perfil: perfil.clave });

  if (r && r.object_key === objectKey) {
    cargaAceptada.add(1, { perfil: perfil.clave, estado: r.processing_status });
  }

  think();
}

export function observar(data) {
  bucleObservacion(data);
}

function recursoEnArbol(data, resourceId) {
  const modulos = (data.version && data.version.modules) || [];
  for (const m of modulos) {
    for (const u of m.units || []) {
      for (const r of u.resources || []) {
        if (r.id === resourceId) return r;
      }
    }
  }
  return null;
}

export function handleSummary(data) {
  const nivel = (__ENV && __ENV.LEVEL) || ESC2_LEVELS[0].id;
  const m = (n) => (data.metrics[n] && data.metrics[n].values) || {};
  return {
    stdout:
      `\n--- Escenario 2a · carga directa · nivel ${nivel} ---\n` +
      `cargas aceptadas   : ${m('upload_accepted_total').count || 0}\n` +
      `task_id vacio      : ${m('upload_task_id_empty_total').count || 0}\n` +
      `transferencias mal : ${m('upload_transfer_failed_total').count || 0}\n` +
      `functional_error_rate: ${m('functional_error_rate').rate}\n` +
      `validacion rate    : ${m('functional_validation_rate').rate}\n`,
  };
}

// El drenaje NO necesita un archivo de manifiesto con las claves: las descubre
// por sí mismo en el detalle de los cursos borrador, filtrando por el prefijo
// k6/<RUN_ID>/ y leyendo el milisegundo de encolado de la propia clave. Así el
// drenaje funciona aunque esta corrida haya fallado a medias.
