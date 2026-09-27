// Cliente HTTP de la API y clasificación de respuestas.
//
// La distinción central es entre tres categorías, y no basta con mirar el
// código HTTP:
//   - ok        : la operación funcionó
//   - negocio   : rechazo esperado por regla de negocio (intentos agotados,
//                 inscripción existente, progreso declarado por el cliente...)
//   - funcional : fallo real (5xx, 4xx inesperado, respuesta malformada)
// El enunciado pide separar las dos últimas, así que cada respuesta se cuenta en
// una métrica y solo la funcional dispara umbral de abortar.

import http from 'k6/http';
import { Counter, Rate, Trend } from 'k6/metrics';
import { BASE_URL, INSECURE_TLS } from './config.js';

export const OPS = {
  catalogo: 'catalogo',
  detalleCurso: 'detalle_curso',
  progreso: 'progreso_curso',
  contenido: 'autoriza_descarga',
  inscripcion: 'inscripcion',
  heartbeat: 'heartbeat',
  quiz: 'quiz',
  manifiesto: 'manifiesto_hls',
  segmento: 'segmento_hls',
  // Escenario 2
  authCarga: 'autoriza_carga',
  transferencia: 'transferencia_objeto',
  confirmar: 'confirma_carga',
  // Admin
  cola: 'cola_admin',
};

export const errorFuncional = new Rate('functional_error_rate');
export const rechazoNegocio = new Rate('business_rejection_rate');
export const respuestas = new Counter('api_responses_total');

// Rechazos esperados por regla de negocio, por operación. Un 409 de intentos
// agotados no es un fallo de la plataforma: es el comportamiento pedido.
//
// Para autoriza_descarga, 404 significa "este recurso no tiene objeto": un
// recurso de texto, PDF o quiz no tiene archivo que servir, y la API lo
// informa así. Es un 404 esperado, no una degradación.
const NEGOCIO = {
  [OPS.inscripcion]: [200, 409],
  // 403 = el estudiante no está matriculado en el curso del quiz. Con la
  // prematricular no debería ocurrir, pero si ocurre es una regla de negocio.
  [OPS.quiz]: [201, 200, 409, 403],
  [OPS.contenido]: [200, 403, 404],
  [OPS.heartbeat]: [200, 400],
};

export function params(extra) {
  return Object.assign(
    {
      insecureSkipTLSVerify: INSECURE_TLS,
      tags: { prueba: 'carga' },
    },
    extra || {}
  );
}

export function url(path) {
  return `${BASE_URL}${path}`;
}

export function auth(token) {
  return { Authorization: `Bearer ${token}` };
}

// jsonPOST envía un cuerpo JSON. extraHeaders se mezcla con las cabeceras
// base en lugar de reemplazarlas: un `Object.assign` sobre el objeto entero
// perdería el Content-Type y el Authorization.
export function json(method, path, payload, token, op, extraHeaders) {
  const headers = Object.assign(
    { 'Content-Type': 'application/json' },
    token ? auth(token) : {},
    extraHeaders || {}
  );
  const res = http.request(
    method,
    url(path),
    JSON.stringify(payload === null || payload === undefined ? {} : payload),
    params({ headers, tags: { op: op || 'sin_etiqueta' } })
  );
  clasifica(res, op);
  return res;
}

export function get(path, token, op, extraParams) {
  // El body va explícito como null: http.request(method, url, params) tomaría
  // params como cuerpo, lo que descarta cabeceras y etiquetas y hace que k6
  // intente codificar un objeto anidado como formulario.
  const res = http.request(
    'GET',
    url(path),
    null,
    params(
      Object.assign(
        { headers: token ? auth(token) : {}, tags: { op: op || 'sin_etiqueta' } },
        extraParams || {}
      )
    )
  );
  clasifica(res, op);
  return res;
}

// bytesSubidos mide la transferencia de un objeto sin pasar por la API. Se
// clasifica aparte porque un fallo de red contra el almacenamiento no es un
// fallo funcional de la plataforma, y el enunciado los pide separados.
export function transfer(urlFirmada, payload, op, extraTags) {
  const res = http.put(
    urlFirmada,
    payload,
    params({ tags: Object.assign({ op: op || 'transferencia_objeto' }, extraTags || {}) })
  );
  return res;
}

// clasifica cuenta la respuesta en la categoría correcta. El status 2xx nunca se
// da por bueno sin más: la validación del contenido la hace cada escenario con
// `check`, porque un 200 con el cuerpo equivocado tampoco es carga funcional
// exitosa.
export function clasifica(res, op) {
  respuestas.add(1, { op: op || 'sin_etiqueta' });

  if (res.status >= 200 && res.status < 300) {
    errorFuncional.add(false, { op: op || 'sin_etiqueta' });
    rechazoNegocio.add(false, { op: op || 'sin_etiqueta' });
    return 'ok';
  }

  const esperados = NEGOCIO[op] || [];
  if (esperados.includes(res.status)) {
    errorFuncional.add(false, { op: op || 'sin_etiqueta' });
    rechazoNegocio.add(true, { op: op || 'sin_etiqueta', status: String(res.status) });
    return 'negocio';
  }

  errorFuncional.add(true, { op: op || 'sin_etiqueta', status: String(res.status) });
  rechazoNegocio.add(false, { op: op || 'sin_etiqueta' });
  return 'funcional';
}

// Latencias propias de los escenarios. El enunciado exige separar el tráfico de
// control (el que va contra la API) de la transferencia de archivos (que va
// directo al almacenamiento de objetos y no toca la API), así que no basta con
// la latencia genérica que k6 mide por petición.
export const latenciaControl = new Trend('ctrl_latency_ms');
export const latenciaTransferencia = new Trend('xfer_latency_ms');
export const latenciaSegmento = new Trend('seg_latency_ms');
export const throughputTransferencia = new Trend('xfer_bytes');

// Body JSON o null si no parsea. Un body inválido en un 2xx se reporta como
// fallo funcional porque significa que la respuesta no es la que se esperaba.
export function body(res) {
  try {
    return res.json();
  } catch (_e) {
    errorFuncional.add(true, { op: 'body_no_json', status: String(res.status) });
    return null;
  }
}
