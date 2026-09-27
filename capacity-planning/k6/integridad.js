// Integridad e idempotencia bajo concurrencia.
//
// El enunciado lo pide de forma explícita: "se conservan la integridad de
// intentos, la calificación y el progreso bajo concurrencia? Incluya una
// comprobación de envío duplicado sin doble calificación".
//
// Corre aparte, con 1 o 2 VU y umbrales que abortan. No se mezcla con la carga
// porque altera el estado de los intentos que se están midiendo y contaminaría
// las tasas de la corrida de capacidad.
//
// Cada comprobación tiene un Counter que debe quedar en cero. Un valor distinto
// de cero es un hallazgo, no un ruido estadístico.

import http from 'k6/http';
import { check } from 'k6';
import { Counter } from 'k6/metrics';
import { RUN_ID, SEED_PASSWORD } from './lib/config.js';
import { OPS, body, get, json, params, url } from './lib/api.js';
import { abrirDataset } from './lib/corpus.js';

const IDENTIDAD = abrirDataset('estudiantes.json');
const CONTENIDO = abrirDataset('cursos.json');

export const options = {
  scenarios: {
    integridad: {
      executor: 'per-vu-iterations',
      vus: 1,
      iterations: 1,
      maxDuration: '10m',
      exec: 'comprobaciones',
      tags: { escenario: 'integridad' },
    },
  },
  thresholds: {
    // Cualquier valor distinto de cero falla la corrida: son invariantes, no
    // métricas de desempeño.
    double_grade_total: ['count==0'],
    duplicated_attempt_total: ['count==0'],
    duplicate_enrollment_total: ['count==0'],
    progress_regression_total: ['count==0'],
    'functional_error_rate': ['rate<0.01'],
  },
  summaryTrendStats: ['p(50)', 'p(95)', 'p(99)'],
};

const dobleCalificacion = new Counter('double_grade_total');
const intentoDuplicado = new Counter('duplicated_attempt_total');
const matriculaDuplicada = new Counter('duplicate_enrollment_total');
const progresoRetrocede = new Counter('progress_regression_total');
const comprobacionesOk = new Counter('integrity_checks_passed_total');

// Estudiante dedicado a la corrida.
//
// Las invariantes se comprueban sobre intentos, y un intento consumido por una
// corrida anterior falsea la comprobación: con un estudiante fijo, la segunda
// corrida de este guion fallaría por los intentos que la primera agotó, y eso
// parecería un defecto de la plataforma cuando no lo es. Se registra entonces
// una cuenta propia por corrida, que empieza sin intentos.
function estudianteLimpio(runId) {
  const username = `integ-${runId}`;
  const password = SEED_PASSWORD;
  const email = `${username}@carga.test`;

  const res = http.post(
    url('/api/v1/auth/register'),
    JSON.stringify({ username, email, password, role: 'student' }),
    params({ headers: { 'Content-Type': 'application/json' }, tags: { op: 'integ_registro' } })
  );
  if (res.status !== 201 && res.status !== 409) {
    throw new Error(`registrar estudiante de integridad: ${res.status} ${res.body}`);
  }
  const resL = http.post(
    url('/api/v1/auth/login'),
    JSON.stringify({ username, password }),
    params({ headers: { 'Content-Type': 'application/json' }, tags: { op: 'integ_login' } })
  );
  const token = (resL.json() || {}).token;
  if (!token) throw new Error(`login del estudiante de integridad: ${resL.status} ${resL.body}`);
  return { username, token };
}

export function setup() {
  const curso = (CONTENIDO.cursos || [])[0];
  if (!curso) throw new Error('datasets/cursos.json sin cursos. Corre antes: task seed:courses');
  const prof = IDENTIDAD.profesores[0];
  const estudiante = estudianteLimpio(RUN_ID);
  return {
    runId: RUN_ID,
    adminToken: IDENTIDAD.admin.token,
    curso,
    // Un estudiante nuevo por corrida, para que los intentos no se arrastren.
    estudiante,
    profesor: prof,
  };
}

function nuevoIntento(quiz, token, sufijo) {
  const clave = `${RUN_ID}-integridad-${sufijo}`;
  const res = json('POST', `/api/v1/quizzes/${quiz.id}/attempts`, null, token, OPS.quiz,
    { 'Idempotency-Key': clave });
  return res.status === 201 ? body(res) : null;
}

function acertar(intento, quiz, token) {
  const respuestas = {};
  (intento.questions || []).forEach((p, i) => {
    respuestas[String(i)] = quiz.correctas[i] !== undefined ? quiz.correctas[i] : 0;
  });
  json('PUT', `/api/v1/attempts/${intento.id}`, { answers: respuestas }, token, OPS.quiz);
}

export function comprobaciones(data) {
  const quiz = data.curso.quizzes[0];
  const token = data.estudiante.token;
  if (!quiz) return;

  // El estudiante debe estar matriculado para que el quiz sea visible.
  json('POST', '/api/v1/enrollments', { course_id: data.curso.id }, token, OPS.inscripcion);

  // 1. Envío duplicado: el mismo submit dos veces no puede calificar dos veces.
  {
    const intento = nuevoIntento(quiz, token, 'dup-submit');
    if (intento) {
      acertar(intento, quiz, token);
      const r1 = json('POST', `/api/v1/attempts/${intento.id}/submit`, null, token, OPS.quiz);
      const b1 = body(r1);
      const r2 = json('POST', `/api/v1/attempts/${intento.id}/submit`, null, token, OPS.quiz);
      const b2 = body(r2);
      // Ojo: check() pasa (valor, nombre_del_check), así que las dos respuestas
      // se comparan dentro de la condición usando el cierre, no como segundo
      // argumento. Tomar el nombre por un segundo body hacía fallar esta
      // comprobación aunque la plataforma se comportara bien.
      const ok = check(true, {
        'submit duplicado: ambas respuestas devuelven la misma calificacion': () =>
          !!b1 && !!b2 && b1.score === b2.score && b1.score === 100,
        'submit duplicado: el estado sigue submitted': () =>
          !!b1 && !!b2 && b2.status === 'submitted',
      });
      if (ok) {
        // Tercero: el GET debe seguir igual, es decir el estado no cambió.
        const r3 = get(`/api/v1/attempts/${intento.id}`, token, OPS.quiz);
        const b3 = body(r3);
        const igual = b3 && b3.score === b1.score && b3.status === 'submitted';
        if (igual) {
          comprobacionesOk.add(1);
        } else {
          dobleCalificacion.add(1);
        }
      } else {
        dobleCalificacion.add(1);
      }
    }
  }

  // 2. Idempotency-Key repetida: debe devolver el mismo intento, no crear otro.
  {
    const clave = `${RUN_ID}-integridad-clave-fija`;
    const a = json('POST', `/api/v1/quizzes/${quiz.id}/attempts`, null, token, OPS.quiz,
      { 'Idempotency-Key': clave });
    const b = json('POST', `/api/v1/quizzes/${quiz.id}/attempts`, null, token, OPS.quiz,
      { 'Idempotency-Key': clave });
    const ba = body(a);
    const bb = body(b);
    const ok = check(true, {
      // Misma razón que arriba: la comparación va dentro de la condición.
      'idempotencia: la misma clave devuelve el mismo intento': () =>
        !!ba && !!bb && ba.id === bb.id,
    });
    if (ok) {
      comprobacionesOk.add(1);
    } else {
      intentoDuplicado.add(1);
    }
  }

  // 3. Inscripción idempotente: repetirla no duplica la fila ni borra progreso.
  {
    const antes = get('/api/v1/enrollments', token, OPS.inscripcion);
    const b1 = body(antes);
    const totalAntes = b1 ? b1.total : -1;
    const r = json('POST', '/api/v1/enrollments', { course_id: data.curso.id }, token, OPS.inscripcion);
    const despues = get('/api/v1/enrollments', token, OPS.inscripcion);
    const b2 = body(despues);
    const ok = check(b2, {
      // El segundo POST responde 200 (idempotente) y el total no crece.
      'inscripcion repetida: el total no crece': (x) => x && x.total === totalAntes,
      'inscripcion repetida: responde 200 o 201': () => r.status === 200 || r.status === 201,
    });
    if (ok) {
      comprobacionesOk.add(1);
    } else {
      matriculaDuplicada.add(1);
    }
  }

  // 4. El progreso declarado por el cliente se rechaza con 400.
  {
    const recurso = (data.curso.recursos || [])[0];
    if (recurso) {
      const res = json('POST', '/api/v1/progress/heartbeat',
        { stable_id: recurso.stableId, position_sec: 10, duration_sec: 100, event: 'playing', percent: 100 },
        token, OPS.heartbeat);
      const ok = check(res, {
        'progreso manipulado: el servidor lo rechaza con 400': (r) => r.status === 400,
      });
      if (ok) comprobacionesOk.add(1);
      else progresoRetrocede.add(1);
    }
  }

  // 5. Submit concurrente del mismo intento desde dos peticiones simultáneas.
  //    http.batch las lanza en paralelo, que es la forma de simular la carrera.
  {
    const intento = nuevoIntento(quiz, token, 'concurrente');
    if (intento) {
      acertar(intento, quiz, token);
      const destino = url(`/api/v1/attempts/${intento.id}/submit`);
      const p = params({
        headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
        tags: { op: OPS.quiz },
      });
      const resps = http.batch([
        { method: 'POST', url: destino, body: '{}', params: p },
        { method: 'POST', url: destino, body: '{}', params: p },
      ]);
      const bs = resps.map((r) => (r.json() || {}).score);
      const ok = check(bs, {
        'submit concurrente: ambas_devuelven la misma calificacion':
          (x) => x.length === 2 && x[0] === x[1] && x[0] === 100,
      });
      if (ok) {
        // Y una tercera lectura confirma que no se generó una calificación extra.
        const r3 = get(`/api/v1/attempts/${intento.id}`, token, OPS.quiz);
        const b3 = body(r3);
        if (b3 && b3.score === 100) {
          comprobacionesOk.add(1);
        } else {
          dobleCalificacion.add(1);
        }
      } else {
        dobleCalificacion.add(1);
      }
    }
  }

}

export function handleSummary(data) {
  const m = (n) => (data.metrics[n] && data.metrics[n].values) || {};
  const fallos = ['double_grade_total', 'duplicated_attempt_total',
    'duplicate_enrollment_total', 'progress_regression_total']
    .reduce((acc, n) => acc + (m(n).count || 0), 0);
  return {
    stdout:
      '\n--- Integridad e idempotencia ---\n' +
      `comprobaciones_ok : ${m('integrity_checks_passed_total').count || 0}\n` +
      `doble calificacion: ${m('double_grade_total').count || 0}\n` +
      `intento duplicado : ${m('duplicated_attempt_total').count || 0}\n` +
      `matricula duplicada: ${m('duplicate_enrollment_total').count || 0}\n` +
      `progreso rechazado mal: ${m('progress_regression_total').count || 0}\n` +
      (fallos === 0
        ? 'OK: ninguna invariante de integridad fue violada.\n'
        : `FALLO: ${fallos} invariantes violadas. No usar esta corrida como evidencia de capacidad.\n`),
  };
}
