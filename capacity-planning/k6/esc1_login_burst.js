// Variante separada: ráfaga de inicios de sesión.
//
// El enunciado pide que, si se evalúa un arranque de sesión, se ejecute y se
// reporte como variante para no atribuir su costo a toda la actividad académica.
// El motivo es concreto: login verifica bcrypt (coste 10) en el request path, de
// modo que mezclarlo en el recorrido principal haría que la medición respondiera
// al costo del hash y no al de la plataforma.
//
// Se ejecuta como guion aparte, y no como escenario anidado del escenario 1,
// para que la serie de login no se mezcle con la de la actividad académica.

import http from 'k6/http';
import exec from 'k6/execution';
import { check } from 'k6';
import { Counter, Trend } from 'k6/metrics';
import { RUN_ID, SEED_PASSWORD } from './lib/config.js';
import { clasifica, params, url } from './lib/api.js';
import { abrirDataset } from './lib/corpus.js';

const IDENTIDAD = abrirDataset('estudiantes.json');

export const options = {
  scenarios: {
    rafaga: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '20s', target: 10 },
        { duration: '1m', target: 30 },
        { duration: '15s', target: 0 },
      ],
      gracefulRampDown: '15s',
      gracefulStop: '30s',
      exec: 'iniciarSesion',
      tags: { escenario: 'login_burst' },
    },
  },
  thresholds: {
    functional_error_rate: ['rate<0.01'],
  },
  summaryTrendStats: ['p(50)', 'p(90)', 'p(95)', 'p(99)', 'max', 'min', 'avg'],
};

const bcryptLogin = new Trend('login_ms');
const credencialesOk = new Counter('login_success_total');
const credencialesRechazadas = new Counter('login_rejected_total');

export function setup() {
  return { estudiantes: IDENTIDAD.estudiantes, runId: RUN_ID };
}

export function iniciarSesion(data) {
  // Cada VU usa una cuenta distinta: repetir la misma credencial mediría la
  // caché de sesión y no el arranque de sesión.
  const est = data.estudiantes[exec.vu.idInInstance % data.estudiantes.length];
  const res = http.post(
    url('/api/v1/auth/login'),
    JSON.stringify({ username: est.username, password: SEED_PASSWORD }),
    params({ headers: { 'Content-Type': 'application/json' }, tags: { op: 'login' } })
  );
  clasifica(res, 'login');

  const bien = check(res, {
    'login: 200 con token': (r) => r.status === 200 && !!(r.json() || {}).token,
  });
  if (bien) {
    credencialesOk.add(1);
    bcryptLogin.add(res.timings.duration, { cuenta: 'student' });
  } else if (res.status === 401) {
    // Credenciales inválidas: no debería ocurrir con la carga sintética. Si ocurre
    // es un hallazgo y queda contabilizado aparte, no como rechazo de negocio.
    credencialesRechazadas.add(1);
  }
}

export function handleSummary(data) {
  const m = (n) => (data.metrics[n] && data.metrics[n].values) || {};
  return {
    stdout:
      '\n--- Variante: ráfaga de inicios de sesión ---\n' +
      `logins_ok             : ${m('login_success_total').count || 0}\n` +
      `logins_rechazados     : ${m('login_rejected_total').count || 0}\n` +
      `functional_error_rate : ${m('functional_error_rate').rate}\n`,
  };
}
