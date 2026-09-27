// Escenario 2d. Drenaje de la cola y verificación del resultado.
//
// Corre después de la carga y sirve para lo que el enunciado más insiste: un
// HTTP de aceptación de carga no equivale a una transcodificación exitosa.
//
// Lo que mide:
//   - espera en cola, por la antigüedad del pendiente más viejo
//   - tiempo desde la carga completa hasta processing_status=ready, calculado en
//     el cliente como (instante en que se vio ready) menos (el milisegundo que
//     el escenario de carga escribió en la clave del objeto)
//   - profundidad de la cola a lo largo del drenado, para ver si converge
//   - que todo trabajo aceptado termine en ready o en un failed diagnosticable
//
// No necesita manifiesto de cargas: descubre los objetos de la corrida por el
// prefijo k6/<RUN_ID>/ en el detalle de los cursos borrador, y así funciona
// aunque la corrida de carga haya fallado a medias.
//
// El descubrimiento se hace UNA sola vez, en setup(). Después cada muestra
// consulta el recurso con GET /api/v1/resources/:id en lugar de releer el árbol
// del curso. Antes de que ese endpoint existiera había que releer el árbol
// completo en cada sondeo, y con un curso que acumula decenas de miles de
// recursos eso eran decenas de MB por muestra: el observador se convertía en
// parte de la carga que pretendía medir.

import { sleep } from 'k6';
import { Counter, Trend } from 'k6/metrics';
import { RUN_ID } from './lib/config.js';
import { OPS, body, get } from './lib/api.js';
import { abrirDataset, epochDeClave } from './lib/corpus.js';
import { observarCola } from './lib/observacion.js';

const IDENTIDAD = abrirDataset('estudiantes.json');
const CONTENIDO = abrirDataset('cursos.json');

// Estabilidad de la cola: se considera drenada cuando el pendiente y el activo
// llevan varias muestras en cero.
const ESTABILIDAD_MUESTRAS = 3;
const INTERVALO_S = 10;

export const options = {
  scenarios: {
    drenaje: {
      executor: 'constant-vus',
      vus: 1,
      duration: __ENV.DURACION || '20m',
      exec: 'drenar',
      tags: { escenario: 'drenaje' },
    },
  },
  summaryTrendStats: ['p(50)', 'p(90)', 'p(95)', 'p(99)', 'max', 'min', 'avg'],
};

const timeToAvailable = new Trend('job_time_to_available_ms');
const profundidad = new Trend('drain_queue_pending');
const recursosListos = new Counter('drain_resources_ready_total');
const recursosFallidos = new Counter('drain_resources_failed_total');
const sinConverger = new Counter('drain_not_converged_total');

// Se recuerda el estado ya contabilizado para no contar el mismo recurso en cada
// muestra. Es estado local del VU y el drenaje corre con un solo VU.
const yaContado = {};

export function setup() {
  const borradores = CONTENIDO.borradores || [];
  if (!borradores.length) {
    throw new Error('datasets/cursos.json no tiene borradores. Corre antes: task seed:courses');
  }
  // El descubrimiento de recursos se hace UNA sola vez, leyendo el árbol de cada
  // borrador. Antes se releía el árbol completo en cada muestra, y con un curso
  // que acumulaba decenas de miles de recursos eso eran decenas de MB por
  // sondeo: el propio observador se convertía en carga. A partir de aquí cada
  // muestra consulta solo el recurso, con GET /api/v1/resources/:id.
  const prefijo = `k6/${RUN_ID}/`;
  const recursos = [];
  for (const cursoId of borradores.map((b) => b.cursoId)) {
    const resC = get(`/api/v1/courses/${cursoId}`, IDENTIDAD.admin.token, 'drenaje_descubre');
    if (resC.status !== 200) continue;
    for (const r of recorrerRecursos(body(resC))) {
      // Solo los objetos de esta corrida, para no contar los del seed.
      if (!r.object_key || r.object_key.indexOf(prefijo) !== 0) continue;
      recursos.push({ id: r.id, objectKey: r.object_key });
    }
  }

  return {
    adminToken: IDENTIDAD.admin.token,
    runId: RUN_ID,
    recursoIds: recursos.map((r) => r.id),
    encoladoPorId: recursos.reduce((acc, r) => {
      const e = epochDeClave(r.objectKey);
      if (e) acc[String(r.id)] = e;
      return acc;
    }, {}),
    limiteMs: Number(__ENV.DURACION_MIN || 20) * 60 * 1000,
  };
}

export function drenar(data) {
  const t0 = Date.now();
  let muestrasEstables = 0;

  while (Date.now() - t0 < data.limiteMs) {
    const resQ = get('/api/v1/admin/queue', data.adminToken, OPS.cola);
    if (resQ.status === 200) {
      const q = (body(resQ).queues || [])[0] || {};
      profundidad.add(q.pending || 0);
      observarCola(data.adminToken);
      if ((q.pending || 0) === 0 && (q.active || 0) === 0) {
        muestrasEstables += 1;
      } else {
        muestrasEstables = 0;
      }
    }

    for (const id of data.recursoIds) {
      if (yaContado[id]) continue;
      const resR = get(`/api/v1/resources/${id}`, data.adminToken, 'drenaje_recurso');
      if (resR.status !== 200) continue;
      const r = (body(resR) || {}).resource;
      if (!r) continue;

      if (r.processing_status === 'ready') {
        const encolado = data.encoladoPorId[String(id)];
        if (encolado) timeToAvailable.add(Date.now() - encolado);
        recursosListos.add(1);
        yaContado[id] = 'ready';
      } else if (r.processing_status === 'failed') {
        // Fallo diagnosticable: el estado queda en la base y el error en el log
        // del worker. Es un resultado válido del experimento, no un silencio.
        recursosFallidos.add(1);
        yaContado[id] = 'failed';
      }
    }

    if (muestrasEstables >= ESTABILIDAD_MUESTRAS) {
      return;
    }
    sleep(INTERVALO_S);
  }

  // Se agotó el tiempo sin converger. Queda constancia, y eso es evidencia de
  // saturación: es exactamente el dato que el informe necesita.
  sinConverger.add(1);
}

function recorrerRecursos(curso) {
  const salida = [];
  const modulos = (curso.version && curso.version.modules) || [];
  for (const m of modulos) {
    for (const u of m.units || []) {
      for (const r of u.resources || []) salida.push(r);
    }
  }
  return salida;
}

export function handleSummary(data) {
  const m = (n) => (data.metrics[n] && data.metrics[n].values) || {};
  const convergio = (m('drain_not_converged_total').count || 0) === 0;
  return {
    stdout:
      `\n--- Escenario 2d · drenaje · run ${RUN_ID} ---\n` +
      `recursos listos    : ${m('drain_resources_ready_total').count || 0}\n` +
      `recursos fallidos  : ${m('drain_resources_failed_total').count || 0}\n` +
      // Las claves de las estadísticas llevan paréntesis: se accede con
      // notación de corchetes, no con punto.
      `time_to_available  : p50=${m('job_time_to_available_ms')['p(50)'] || 0} ms ` +
      `p95=${m('job_time_to_available_ms')['p(95)'] || 0} ms ` +
      `max=${m('job_time_to_available_ms').max || 0} ms\n` +
      (convergio
        ? 'la cola drenó hasta estado estable.\n'
        : 'AVISO: el drenaje NO convergió dentro del límite. Evidencia de saturación.\n'),
  };
}
