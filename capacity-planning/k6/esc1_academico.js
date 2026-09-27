// Escenario 1. Actividad académica concurrente.
//
// Simula lo que haría un estudiante durante una sesión: mirar el catálogo, abrir
// un curso, matricularse, consultar contenido, registrar progreso válido y
// presentar un quiz. La mezcla combina lecturas y escrituras y es idéntica en
// todos los niveles, que es lo que hace comparables las mediciones.
//
// La autenticación NO es parte del recorrido medido: las sesiones se preparan en
// el seed y la matriculación inicial se hace en setup(), fuera de la ventana
// medida. Login verifica bcrypt en el request path y, de estar en la mezcla,
// mediría el costo del hash y no el de la plataforma. El arranque de sesión se
// mide aparte, en esc1_login_burst.js, y se reporta como variante.

import http from 'k6/http';
import exec from 'k6/execution';
import { sleep } from 'k6';
import { Counter, Trend } from 'k6/metrics';
import {
  ENTORNO, ESC1_ESCALADA, ESC1_LEVELS, PREMATRICULA_LOTE, RUN_ID, escenarioPorNiveles, think,
} from './lib/config.js';
import {
  OPS, body, get, json, latenciaControl, params, rechazoNegocio, url,
} from './lib/api.js';
import {
  validaCalificacion, validaCatalogo, validaDescarga, validaDetalle,
  validaHeartbeat, validaInscripcion, validaIntento, validaProgreso,
} from './lib/checks.js';
import { abrirDataset, estudianteDe } from './lib/corpus.js';
import { bucleObservacion, escenarioObservacion } from './lib/observacion.js';

// open() solo funciona en contexto de init: el corpus se lee una vez al cargar
// el módulo y se pasa a los VU desde setup().
const IDENTIDAD = abrirDataset('estudiantes.json');
const CONTENIDO = abrirDataset('cursos.json');

// Cursos en los que se matricula cada estudiante antes de medir. Con 5 y 9
// quizzes por curso, cada estudiante dispone de 45 quizzes x 3 intentos = 135
// intentos, suficiente para toda una corrida sin agotarlos.
const CURSOS_POR_ESTUDIANTE = Number(__ENV.CURSOS_POR_ESTUDIANTE || 5);

export const options = {
  // setupTimeout: la prematricular de setup() son 2500 matriculaciones en lotes
  // de 250, y cada una es una escritura en Cloud SQL más un JWT. El default de
  // k6 son 60s y no alcanzan; el trabajo no se mide, es preparatorio.
  setupTimeout: '10m',
  scenarios: {
    carga: escenarioPorNiveles(ESC1_LEVELS.concat(ESC1_ESCALADA), 'actividadAcademica'),
    observacion: escenarioObservacion(),
  },
  // El único umbral que aborta es el error funcional. Un rechazo de negocio
  // (intentos agotados, matrícula repetida) no es degradación del sistema.
  thresholds: {
    functional_error_rate: ['rate<0.01'],
  },
  summaryTrendStats: ['p(50)', 'p(90)', 'p(95)', 'p(99)', 'max', 'min', 'avg'],
};

const enrollmentNuevo = new Counter('enroll_new_total');
const enrollmentRepetido = new Counter('enroll_existing_total');
const withdrawal = new Counter('enroll_withdraw_total');
const latenciaQuiz = new Trend('quiz_journey_ms');
// En k6 v2 las métricas Trend no exponen count en el resumen, así que el número
// de ciclos completos se cuenta aparte con un Counter.
const quizCompletos = new Counter('quiz_flows_total');
const prematriculaFallos = new Counter('preenroll_failed_total');

// Peso de cada operación en la mezcla. La suma es 100 y no cambia entre niveles.
const MEZCLA = [
  { op: OPS.catalogo, peso: 30 },
  { op: OPS.detalleCurso, peso: 20 },
  { op: OPS.progreso, peso: 15 },
  { op: OPS.contenido, peso: 10 },
  { op: OPS.inscripcion, peso: 10 },
  { op: OPS.heartbeat, peso: 10 },
  { op: OPS.quiz, peso: 5 },
];

function eligeOperacion() {
  let n = Math.random() * 100;
  for (const m of MEZCLA) {
    n -= m.peso;
    if (n < 0) return m.op;
  }
  return OPS.catalogo;
}

// Deriva del corpus lo que el recorrido necesita, para no recalcularlo en cada
// iteración: los recursos sin quiz (para autorizar descargas) y los que sí
// admiten heartbeat (los quizzes se completan aprobando, no con progreso).
function derivaCurso(c) {
  return Object.assign({}, c, {
    recursosNoQuiz: c.recursos.filter((r) => r.type !== 'quiz'),
    recursosProgreso: c.recursos.filter((r) => r.type !== 'quiz'),
  });
}

// Matrícula inicial antes de medir. Sin esto la consulta de progreso devolvería
// 403 en la mayoría de las iteraciones y el_endpoint_ más pesado del escenario
// quedaría sin medir. No cuenta como carga: ocurre antes de que arranque el
// escenario de carga.
function prematricular(estudiantes, cursos) {
  const requests = estudiantes.map((e, i) => ({
    method: 'POST',
    url: url('/api/v1/enrollments'),
    body: JSON.stringify({ course_id: cursos[i % cursos.length].id }),
    params: params({
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${e.token}` },
      tags: { op: 'prematricula' },
    }),
  }));
  // Se trocea: 500 estudiantes x 5 cursos son 2500 peticiones, y lanzarlas en un
  // solo batch hace que sobre una WAN el setup pueda agotar su tiempo y aborte la
  // corrida entera. Por lotes, un fallo se localiza y el error es más claro.
  let fallos = 0;
  for (let i = 0; i < requests.length; i += PREMATRICULA_LOTE) {
    const lote = requests.slice(i, i + PREMATRICULA_LOTE);
    const resps = http.batch(lote);
    for (const r of resps) {
      if (r.status !== 200 && r.status !== 201) fallos += 1;
    }
    if ((i / PREMATRICULA_LOTE) % 5 === 0) {
      // Log de avance: en GCP el sembrado tarda minutos y sin esto parece colgado.
      console.log(`matriculando ${Math.min(i + PREMATRICULA_LOTE, requests.length)}/${requests.length}`);
    }
  }
  prematriculaFallos.add(fallos);
  return fallos;
}

// Matrícula inicial antes de medir. Sin esto, la consulta de progreso y la de
// descarga devolverían 403 en la mayoría de las iteraciones y el_endpoint_ más
// pesado del quedaría sin medir. No cuenta como carga: ocurre antes de que
// arranque el escenario de carga.
//
// Cada estudiante se matricula en un subconjunto propio de cursos, no en todos.
// Es lo que evita el conflicto artificial que el enunciado prohíbe: si todos
// compartieran curso, un solo par (estudiante, quiz) agotaría sus tres intentos
// en pocas iteraciones y el resto de la corrida mediría 409 en lugar de
// calificación.
function prematricular(estudiantes, cursos, porEstudiante) {
  const requests = [];
  for (let i = 0; i < estudiantes.length; i += 1) {
    const e = estudiantes[i];
    e.cursos = [];
    e.quizzes = [];
    for (let k = 0; k < porEstudiante; k += 1) {
      // El paso 7 es coprimo con el número habitual de cursos, así que no
      // repite dentro de la misma student'se y reparte sin solapar.
      const c = cursos[(i * 7 + k) % cursos.length];
      e.cursos.push(c);
      for (const q of c.quizzes) e.quizzes.push(q);
      requests.push({
        method: 'POST',
        url: url('/api/v1/enrollments'),
        body: JSON.stringify({ course_id: c.id }),
        params: params({
          headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${e.token}` },
          tags: { op: 'prematricula' },
        }),
      });
    }
  }
  // Se trocea: 500 estudiantes x 5 cursos son 2500 peticiones, y lanzarlas en un
  // solo batch hace que sobre una WAN el setup pueda agotar su tiempo y aborte la
  // corrida entera. Por lotes, un fallo se localiza y el error es más claro.
  let fallos = 0;
  for (let i = 0; i < requests.length; i += PREMATRICULA_LOTE) {
    const lote = requests.slice(i, i + PREMATRICULA_LOTE);
    const resps = http.batch(lote);
    for (const r of resps) {
      if (r.status !== 200 && r.status !== 201) fallos += 1;
    }
    if ((i / PREMATRICULA_LOTE) % 5 === 0) {
      // Log de avance: en GCP el sembrado tarda minutos y sin esto parece colgado.
      console.log(`matriculando ${Math.min(i + PREMATRICULA_LOTE, requests.length)}/${requests.length}`);
    }
  }
  prematriculaFallos.add(fallos);
  return fallos;
}

export function setup() {
  if (!IDENTIDAD.estudiantes || !IDENTIDAD.estudiantes.length) {
    throw new Error('datasets/estudiantes.json vacío. Corre antes: task seed:students');
  }
  if (!CONTENIDO.cursos || !CONTENIDO.cursos.length) {
    throw new Error('datasets/cursos.json vacío. Corre antes: task seed:courses');
  }
  const cursos = CONTENIDO.cursos.map(derivaCurso);
  const porEstudiante = Math.max(1, Math.min(CURSOS_POR_ESTUDIANTE, cursos.length));
  const fallos = prematricular(IDENTIDAD.estudiantes, cursos, porEstudiante);
  if (fallos > 0) {
    throw new Error(
      `La prematricular falló en ${fallos} de los ${IDENTIDAD.estudiantes.length} estudiantes. ` +
      'Sin matrícula, el escenario mediría 403 en vez de progreso.'
    );
  }
  return {
    adminToken: IDENTIDAD.admin.token,
    runId: RUN_ID,
    estudiantes: IDENTIDAD.estudiantes,
    cursos,
    totales: {
      estudiantes: IDENTIDAD.estudiantes.length,
      cursos: cursos.length,
      cursosPorEstudiante: porEstudiante,
      intentosDisponiblesPorEstudiante: IDENTIDAD.estudiantes[0]
        ? IDENTIDAD.estudiantes[0].quizzes.length * 3
        : 0,
      recursosPorCurso: cursos[0] ? cursos[0].recursos.length : 0,
      quizzesPorCurso: cursos[0] ? cursos[0].quizzes.length : 0,
    },
  };
}

// Cada VU trabaja con un estudiante fijo, y ese estudiante recorre siempre uno
// de los cursos en los que quedó matriculado en setup(). Fijar la identidad por
// VU es lo que evita conflictos artificiales entre usuarios virtuales y hace el
// reparto reproducible entre corridas.
export function actividadAcademica(data) {
  const est = estudianteDe(data, exec.vu.idInInstance);
  const estCursos = est.cursos;
  const curso = estCursos[exec.vu.iterationInInstance % estCursos.length];
  const op = eligeOperacion();
  const t0 = Date.now();

  switch (op) {
    case OPS.catalogo: catalogo(); break;
    case OPS.detalleCurso: detalleCurso(curso, est.token); break;
    case OPS.progreso: progreso(curso, est.token); break;
    case OPS.contenido: contenido(curso, est.token); break;
    case OPS.inscripcion: inscripcion(curso, est.token); break;
    case OPS.heartbeat: heartbeat(curso, est.token); break;
    case OPS.quiz: quiz(curso, est.token, est.quizzes); break;
    default: catalogo(); break;
  }
  latenciaControl.add(Date.now() - t0, { op });
  think();
}

export function observar(data) {
  bucleObservacion(data);
}

// ---------------------------------------------------------------------------
// Operaciones
// ---------------------------------------------------------------------------

function catalogo() {
  // Se recorre la paginación: repetir siempre page=1 mediría una caché y no el
  // catálogo completo. El catálogo es público, así que va sin token: mandarlo
  // probaría una ruta que un visitante anónimo no exercise.
  const page = 1 + (exec.vu.iterationInInstance % 5);
  const res = get(`/api/v1/courses?page=${page}&limit=20&sort=title%20asc`, null, OPS.catalogo);
  if (res.status === 200) validaCatalogo(body(res));
}

function detalleCurso(curso, token) {
  const res = get(`/api/v1/courses/${curso.id}`, token, OPS.detalleCurso);
  if (res.status === 200) validaDetalle(body(res));
}

function progreso(curso, token) {
  // Esta consulta escribe: actualiza el estado de la matrícula y puede emitir
  // insignia. Es la operación más pesada del escenario 1.
  const res = get(`/api/v1/progress/${curso.id}`, token, OPS.progreso);
  if (res.status === 200) validaProgreso(body(res));
  else if (res.status === 403) {
    // Sin matrícula: rechazo esperado, no degradación.
    rechazoNegocio.add(true, { op: 'progreso_curso', status: '403' });
  }
}

function contenido(curso, token) {
  const recursos = curso.recursosNoQuiz;
  if (!recursos.length) return;
  const r = recursos[exec.vu.iterationInInstance % recursos.length];
  const res = get(`/api/v1/resources/${r.id}/download-url`, token, OPS.contenido);
  if (res.status === 200) validaDescarga(body(res));
}

function inscripcion(curso, token) {
  // Un tercio de las iteraciones retira y vuelve a matricular. Así la operación
  // produce escrituras de verdad (201) y no solo el camino idempotente (200),
  // que es lo que la haría trivialmente barata.
  if (exec.vu.iterationInInstance % 3 === 0) {
    const resW = json('DELETE', `/api/v1/enrollments/${curso.id}`, null, token, OPS.inscripcion);
    if (resW.status === 200) withdrawal.add(1);
  }
  const res = json('POST', '/api/v1/enrollments', { course_id: curso.id }, token, OPS.inscripcion);
  if (res.status === 201) {
    enrollmentNuevo.add(1);
    validaInscripcion(body(res));
  } else if (res.status === 200) {
    // Ya matriculado: la API es idempotente. No es un error.
    enrollmentRepetido.add(1);
  }
}

// El avance por recurso se lleva en estado local del VU. La posición es
// monótona por (estudiante, recurso) porque el servidor nunca la baja, así que
// reiniciarla al cambiar de recurso es correcto.
const avance = {};

function heartbeat(curso, token) {
  const recursos = curso.recursosProgreso;
  if (!recursos.length) return;
  const r = recursos[exec.vu.iterationInInstance % recursos.length];

  const clave = `${exec.vu.idInInstance}:${r.id}`;
  const posicion = (avance[clave] || 0) + 1 + Math.floor(Math.random() * 10);
  avance[clave] = posicion;

  const payload = { stable_id: r.stableId, event: 'playing', position_sec: posicion };
  if (r.type === 'pdf') {
    payload.event = 'pdf_open';
    payload.duration_sec = 0;
    payload.page = 1 + (posicion % 40);
    payload.total_pages = 42;
  } else {
    payload.duration_sec = 600;
  }
  // percent y completed NUNCA se envían: el servidor los rechaza con 400 y deja
  // rastro de auditoría porque el avance lo calcula él, no el cliente.
  const res = json('POST', '/api/v1/progress/heartbeat', payload, token, OPS.heartbeat);
  if (res.status === 200) validaHeartbeat(body(res));
}

function quiz(curso, token, todosLosQuizzes) {
  // Se reparte entre TODOS los quizzes del corpus, no solo los del curso del VU.
  // Cada quiz tiene attempts_allowed=3 y ese límite es por (estudiante, quiz): si
  // un VU rotara solo por los quizzes de su curso agotaría los tres en pocas
  // iteraciones y el resto de la corrida mediría un 409 de intentos agotados en
  // lugar de calificación. El enunciado pide justo evitar ese conflicto.
  if (!todosLosQuizzes.length) return;
  const q = todosLosQuizzes[
    (exec.vu.iterationInInstance + exec.vu.idInInstance * 7) % todosLosQuizzes.length
  ];
  const t0 = Date.now();

  // El idempotency_key tiene índice único GLOBAL y su deduplicación ignora
  // estudiante y quiz: reutilizar una clave devolvería el intento de otro. Por
  // eso la clave combina corrida, VU e iteración.
  const clave = `${RUN_ID}-${exec.vu.idInInstance}-${exec.vu.iterationInInstance}`;

  const res = json('POST', `/api/v1/quizzes/${q.id}/attempts`, null, token, OPS.quiz,
    { 'Idempotency-Key': clave });
  if (res.status !== 201) return; // 409 = intentos agotados, ya clasificado
  const intento = body(res);
  if (!validaIntento(intento)) return;

  // Las respuestas correctas las fijó el seed; se acierta a propósito para que
  // la calificación sea determinista y comparable entre niveles.
  const respuestas = {};
  (intento.questions || []).forEach((pregunta, i) => {
    respuestas[String(i)] = q.correctas[i] !== undefined ? q.correctas[i] : 0;
  });

  const resSave = json('PUT', `/api/v1/attempts/${intento.id}`, { answers: respuestas },
    token, OPS.quiz);
  if (resSave.status !== 200) return;

  const resSubmit = json('POST', `/api/v1/attempts/${intento.id}/submit`, null, token, OPS.quiz);
  if (resSubmit.status === 200) {
    // Tres preguntas acertadas: la calificación debe ser 100.
    validaCalificacion(body(resSubmit), 100);
  }
  latenciaQuiz.add(Date.now() - t0);
  quizCompletos.add(1);
}

export function handleSummary(data) {
  const nivel = (__ENV && __ENV.LEVEL) || ESC1_LEVELS[0].id;
  const m = (nombre) => (data.metrics[nombre] && data.metrics[nombre].values) || {};
  return {
    stdout:
      `\n--- Escenario 1 · ${ENTORNO} · nivel ${nivel} ---\n` +
      `functional_error_rate : ${m('functional_error_rate').rate}\n` +
      `business_rejection_rate: ${m('business_rejection_rate').rate}\n` +
      `functional_validation_rate: ${m('functional_validation_rate').rate}\n` +
      `matriculas nuevas     : ${m('enroll_new_total').count || 0}\n` +
      `matriculas repetidas  : ${m('enroll_existing_total').count || 0}\n` +
      `retiros               : ${m('enroll_withdraw_total').count || 0}\n` +
      `ciclos de quiz        : ${m('quiz_flows_total').count || 0}\n` +
      `latencia quiz p95     : ${m('quiz_journey_ms')['p(95)'] || 0} ms\n` +
      `intentos agotados 409 : ${m('business_rejection_rate').rate || 0} (tasa)\n`,
  };
}
