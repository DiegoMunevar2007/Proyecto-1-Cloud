// Escenario 2c. Consumo de HLS ya disponible.
//
// Simula estudiantes reproduciendo contenido que ya está en el almacenamiento de
// objetos. La cadencia importa: el worker corta segmentos de 6s (`-hls_time 6`),
// así que este guion descarga UN segmento por iteración y espera 6s. Descargar
// todos los segmentos tan rápido como se pueda es otro patrón de carga y se
// identifica aparte con PATRON=burst; nunca se mezcla.
//
// Dos vías que el enunciado pide separar:
//   - manifiesto: GET a la URL que devuelve download-url (tráfico de control,
//     contra la API, porque el bucket hls es de lectura anónima)
//   - segmentos: GET directo al almacenamiento, que no toca la API
//
// No se reporta tiempo hasta el primer cuadro ni interrupciones: el enunciado
// dice que las peticiones HTTP por sí solas no demuestran esas métricas, y no
// se está usando un reproductor.

import http from 'k6/http';
import exec from 'k6/execution';
import { sleep } from 'k6';
import { Counter, Trend } from 'k6/metrics';
import { RUN_ID } from './lib/config.js';
import {
  OPS, body, get, latenciaControl, latenciaSegmento, params,
  throughputTransferencia, url,
} from './lib/api.js';
import { validaDescarga } from './lib/checks.js';
import { abrirDataset } from './lib/corpus.js';

const IDENTIDAD = abrirDataset('estudiantes.json');
const MEDIA = abrirDataset('media.json');

const PATRON = (__ENV && __ENV.PATRON) || 'reproduccion';
// Cadencia por defecto si el manifiesto no expone duración. El guion usa la
// que declara cada manifiesto cuando está disponible.
const SEGUNDO_SEGMENTO_POR_DEFECTO = 6;

export const options = {
  scenarios: {
    consumo: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '30s', target: Number(__ENV.VUS || 10) },
        { duration: '4m', target: Number(__ENV.VUS || 10) },
        { duration: '15s', target: 0 },
      ],
      gracefulRampDown: '15s',
      gracefulStop: '30s',
      exec: 'reproducir',
      tags: { escenario: 'consumo', patron: PATRON },
    },
  },
  thresholds: {
    functional_error_rate: ['rate<0.01'],
  },
  summaryTrendStats: ['p(50)', 'p(90)', 'p(95)', 'p(99)', 'max', 'min', 'avg'],
};

const manifiestosOk = new Counter('hls_manifest_ok_total');
const segmentosOk = new Counter('hls_segment_ok_total');
const segmentosError = new Counter('hls_segment_error_total');
const latenciaManifiesto = new Trend('hls_manifest_ms');
const segmentosPorManifiesto = new Counter('hls_segments_found_total');

// Índice de segmento por recurso, para avanzar por la lista como lo haría un
// reproductor. Es estado local del VU: cada VU recorre su propio punto.
const cursor = {};

export function setup() {
  const listos = (MEDIA.subidos || []).filter((s) => s.hlsUrl);
  if (!listos.length) {
    throw new Error(
      'datasets/media.json no tiene recursos listos. Corre antes: task seed:media ' +
      '(y revisa que el worker procese lo que sube).'
    );
  }
  const cursoMedioId = MEDIA.cursoMedioId;
  if (!cursoMedioId) {
    throw new Error('datasets/media.json no tiene cursoMedioId: el seed es anterior a ese campo.');
  }

  // El consumo exige estar matriculado en un curso publicado. La matriculación
  // se hace aquí, en setup(), para no contarla como carga medida.
  const estudiantes = IDENTIDAD.estudiantes.slice(0, Number(__ENV.ESTUDIANTES_CONSUMO || 20));
  http.batch(estudiantes.map((e) => ({
    method: 'POST',
    url: url('/api/v1/enrollments'),
    body: JSON.stringify({ course_id: cursoMedioId }),
    params: params({
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${e.token}` },
      tags: { op: 'prematricula_media' },
    }),
  })));

  return {
    adminToken: IDENTIDAD.admin.token,
    runId: RUN_ID,
    cursoMedioId,
    recursos: listos,
    estudiantes,
  };
}

export function reproducir(data) {
  const idx = exec.vu.idInInstance % data.recursos.length;
  const r = data.recursos[idx];
  const est = data.estudiantes[exec.vu.idInInstance % data.estudiantes.length];

  // 1. Autorización: la API decide si este estudiante puede ver este recurso.
  const resD = get(`/api/v1/resources/${r.resourceId}/download-url`, est.token, OPS.contenido);
  if (resD.status !== 200) {
    // 403 = no matriculado, rechazo esperado y ya clasificado por la API.
    return;
  }
  const autorizado = body(resD);
  if (!validaDescarga(autorizado)) return;
  if (autorizado.hls !== true) return; // aún no está listo: no cuenta como error

  // 2. Manifiesto. La URL es la que devolvió la API y no una constante del guion,
  //    así el guion no necesita conocer S3_PUBLIC_ENDPOINT.
  const t0 = Date.now();
  const resM = http.get(autorizado.url,
    params({ tags: { op: OPS.manifiesto } }));
  latenciaManifiesto.add(resM.timings.duration, { perfil: r.perfil });
  latenciaControl.add(Date.now() - t0, { op: 'autoriza_y_manifiesto' });
  if (resM.status !== 200) {
    segmentosError.add(1, { tipo: 'manifiesto', status: String(resM.status) });
    return;
  }
  manifiestosOk.add(1, { perfil: r.perfil });

  const nombres = segmentosDe(resM.body);
  if (!nombres.length) {
    segmentosError.add(1, { tipo: 'sin_segmentos' });
    return;
  }
  segmentosPorManifiesto.add(nombres.length, { perfil: r.perfil });

  // La cadencia de reproducción usa la duración que declara el propio manifiesto,
  // no un valor supuesto: es lo que separa "reproducir" de "bombear".
  const duracion = duracionDeSegmento(resM.body) || SEGUNDO_SEGMENTO_POR_DEFECTO;

  // 3. Un segmento por iteración, avanzando por la lista.
  const clave = `${exec.vu.idInInstance}:${r.resourceId}`;
  const i = (cursor[clave] || 0) % nombres.length;
  cursor[clave] = i + 1;

  const base = autorizado.url.slice(0, autorizado.url.lastIndexOf('/') + 1);
  const t1 = Date.now();
  const resS = http.get(base + nombres[i],
    params({ tags: { op: OPS.segmento, perfil: r.perfil } }));
  latenciaSegmento.add(resS.timings.duration, { perfil: r.perfil });
  throughputTransferencia.add(resS.body.length, { via: 'segmento' });

  if (resS.status === 200) {
    segmentosOk.add(1, { perfil: r.perfil });
  } else {
    segmentosError.add(1, { tipo: 'segmento', status: String(resS.status) });
  }

  // En modo burst se descarga todo sin esperar y se declara como otro patrón.
  if (PATRON !== 'burst') {
    sleep(duracion);
  }
}

// Lee la duración del segmento de la cabecera #EXTINF del manifiesto.
function duracionDeSegmento(manifiesto) {
  const m = /#EXTINF:([0-9.]+)/.exec(String(manifiesto || ''));
  return m ? Number(m[1]) : null;
}

// Extrae los nombres de segmento de las líneas que no son comentario del m3u8.
function segmentosDe(manifiesto) {
  const out = [];
  String(manifiesto || '').split(/\r?\n/).forEach((linea) => {
    const l = linea.trim();
    if (l && l.charAt(0) !== '#') out.push(l);
  });
  return out;
}

export function handleSummary(data) {
  const m = (n) => (data.metrics[n] && data.metrics[n].values) || {};
  return {
    stdout:
      `\n--- Escenario 2c · consumo HLS · patron ${PATRON} ---\n` +
      `manifiestos ok : ${m('hls_manifest_ok_total').count || 0}\n` +
      `segmentos ok   : ${m('hls_segment_ok_total').count || 0}\n` +
      `errores        : ${m('hls_segment_error_total').count || 0}\n` +
      `functional_error_rate: ${m('functional_error_rate').rate}\n`,
  };
}
