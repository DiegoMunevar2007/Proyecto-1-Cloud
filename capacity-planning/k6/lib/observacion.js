// Escenario observador: sondea el estado de la cola durante toda la corrida.
//
// El generador de carga corre fuera de las VMs y el firewall solo deja leer
// /api/v1/admin/queue, así que la profundidad, la antigüedad y la tasa de la
// cola llegan por ese endpoint, que devuelve JSON. /metrics quedó atado a
// loopback en cada VM para el Ops Agent, de modo que k6 no puede leerlo: las
// métricas de CPU y memoria de las máquinas viven en Cloud Monitoring, no aquí.
//
// El sondeo se hace a 1 VU con un intervalo fijo. Son 5 muestras por minuto
// frente a los miles de requests de la corrida, así que su peso en la medición
// es despreciable, pero conviene declararlo.

import { sleep } from 'k6';
import { Counter, Gauge, Trend } from 'k6/metrics';
import { get, OPS } from './api.js';
import { validaCola } from './checks.js';

// Métricas del sondeo. Ningún guion las importa: se publican para que aparezcan
// en el resumen de k6, así que no se exportan.
const queuePending = new Gauge('queue_pending');
const queueActive = new Gauge('queue_active');
const queueAge = new Gauge('queue_age_seconds');
const queueRetry = new Gauge('queue_retry');
const queueArchived = new Gauge('queue_archived');
const queueSize = new Gauge('queue_size');
const queueScrapeOk = new Gauge('queue_scrape_ok');
const queueProcessedTotal = new Counter('queue_processed_total');
const queueFailedTotal = new Counter('queue_failed_total');
const queueLatency = new Trend('queue_poll_latency_ms');
const queuePollErrors = new Counter('queue_poll_errors_total');

const INTERVALO_S = 5;

// Nótese que el scraping de la cola también lo hace la API en su /metrics, con
// un collector propio. Esta ruta JSON existe para que la serie quede en el
// mismo archivo que las métricas de k6 y se correlacione por marca de tiempo
// sin depender de Cloud Monitoring.
export function observarCola(tokenAdmin) {
  const res = get('/api/v1/admin/queue', tokenAdmin, OPS.cola);

  if (res.status !== 200) {
    queuePollErrors.add(1, { status: String(res.status) });
    queueScrapeOk.add(0);
    return;
  }
  const data = res.json();
  validaCola(data);
  const q = (data.queues && data.queues[0]) || null;
  if (!q) {
    queueScrapeOk.add(0);
    return;
  }
  queueScrapeOk.add(1);
  queuePending.add(q.pending, { cola: q.queue });
  queueActive.add(q.active, { cola: q.queue });
  queueAge.add(q.age_oldest_pending_sec, { cola: q.queue });
  queueRetry.add(q.retry, { cola: q.queue });
  queueArchived.add(q.archived, { cola: q.queue });
  queueSize.add(q.size, { cola: q.queue });
  queueProcessedTotal.add(q.processed_total, { cola: q.queue });
  queueFailedTotal.add(q.failed_total, { cola: q.queue });
  queueLatency.add(res.timings.duration);
}

// Devuelve el escenario listo para incluir en `options.scenarios`.
//
// k6 pasa a `exec` el mismo valor que devuelve `setup()`, así que el token de
// admin llega como `data.adminToken` y no hace falta pasarlo por el entorno.
// Cada guion que incluya este escenario debe exportar su propio `observar` que
// delegue en `bucleObservacion`.
//
// El esquema de un escenario es estricto: k6 rechaza campos propios, así que el
// intervalo se ajusta con la constante del módulo y no con una clave aquí.
export function escenarioObservacion() {
  return {
    executor: 'constant-vus',
    vus: 1,
    // Por defecto acompaña a la corrida más larga. Se puede acortar para pruebas
    // de humo, donde interesa validar el código y no la serie de tiempo.
    duration: __ENV.OBSERVACION_DURACION || '20m',
    exec: 'observar',
    tags: { escenario: 'observacion' },
  };
}

export function bucleObservacion(data) {
  const token = data && data.adminToken;
  if (!token) {
    queuePollErrors.add(1, { motivo: 'sin_admin_token' });
    sleep(INTERVALO_S);
    return;
  }
  observarCola(token);
  sleep(INTERVALO_S);
}
