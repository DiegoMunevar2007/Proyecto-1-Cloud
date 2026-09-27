// Escenario 2b. Sub-escenario TUS (carga reanudable a través de la API).
//
// Se reporta por separado del 2a y no se mezcla con él, porque son dos patrones
// de carga distintos que miden cosas distintas:
//
//   2a presigned PUT : los bytes van del cliente al almacenamiento. La API solo
//                      autoriza. Mide el almacenamiento.
//   2b TUS           : los bytes ATRAVIESAN la API, que los reenvía al
//                      almacenamiento como UploadPart. Mide el API y el
//                      almacenamiento a la vez.
//
// Confundirlos haría creer que la API es el cuello cuando en realidad el límite
// es el canal de subida contra el bucket.
//
// Detalles del protocolo que hay que respetar:
//   - Upload-Metadata va en base64 por valor.
//   - El id que devuelve Location tiene forma <uuid>+<multipartId>; el + hay que
//     codificarlo en la URL.
//   - PATCH devuelve 204 y el nuevo Upload-Offset; un offset equivocado da 409.

import http from 'k6/http';
import exec from 'k6/execution';
import { b64encode } from 'k6/encoding';
import { Counter, Trend } from 'k6/metrics';
import { ESC2_LEVELS, RUN_ID, TUS_HABILITADO, escenarioPorNiveles, think } from './lib/config.js';
import {
  body, json, latenciaControl, latenciaTransferencia, params,
  throughputTransferencia, url,
} from './lib/api.js';
import { abrirDataset, abrirMedio } from './lib/corpus.js';
import { bucleObservacion, escenarioObservacion } from './lib/observacion.js';

const IDENTIDAD = abrirDataset('estudiantes.json');
const CONTENIDO = abrirDataset('cursos.json');

// TUS exige que el tamaño total se conozca de antemano.
const PERFILES = [
  { clave: 'p1-small', archivo: 'p1-small.mp4', mime: 'video/mp4', tipo: 'video' },
  { clave: 'a1-audio', archivo: 'a1-audio.m4a', mime: 'audio/mp4', tipo: 'audio' },
];

// Tamaño del trozo con el que se hace cada PATCH. El protocolo es reanudable y
// este es el grano con el que se está midiendo, así que se declara.
const TROZO = 4 * 1024 * 1024;

export const options = {
  scenarios: {
    carga: escenarioPorNiveles(ESC2_LEVELS, 'cargaTus'),
    observacion: escenarioObservacion(),
  },
  thresholds: {
    functional_error_rate: ['rate<0.01'],
  },
  summaryTrendStats: ['p(50)', 'p(90)', 'p(95)', 'p(99)', 'max', 'min', 'avg'],
};

const creados = new Counter('tus_created_total');
const patches = new Counter('tus_patch_total');
const erroresOffset = new Counter('tus_offset_conflict_total');
const fallidos = new Counter('tus_failed_total');
const latenciaCreate = new Trend('tus_create_ms');
const latenciaPatch = new Trend('tus_patch_ms');

const CUERPOS = {};
for (const p of PERFILES) {
  CUERPOS[p.clave] = abrirMedio(p.archivo);
}

// Sondeo previo: una carga TUS completa antes de medir nada.
//
// El protocolo TUS contra la API S3-compatible de GCS es la integración más
// frágil del despliegue (tusd reenvía los bytes como UploadPart y se ha
// documentado el riesgo con UsePathStyle). Si no funciona, es mejor saberlo con
// un mensaje explícito que descubrir 2 VUs fallando durante cuatro minutos y
// confundirlo con saturación.
function sondeoPrevio(perfil, bytes, borrador, prof) {
  const resR = json('POST', `/api/v1/units/${borrador.unidadId}/resources`,
    { type: perfil.tipo, title: `tus sondeo ${RUN_ID}` }, prof.token, 'crea_recurso');
  if (resR.status !== 201) {
    return `no se pudo crear el recurso del sondeo: ${resR.status} ${resR.body}`;
  }
  const recurso = (resR.json() || {}).resource;
  const metadata = [
    `resource_id ${b64encode(String(recurso.id))}`,
    `filetype ${b64encode(perfil.mime)}`,
  ].join(',');
  const resC = http.post(url('/api/v1/uploads'), null, params({ headers: {
    Authorization: `Bearer ${prof.token}`,
    'Tus-Resumable': '1.0.0',
    'Upload-Length': String(bytes.byteLength),
    'Upload-Metadata': metadata,
  }, tags: { op: 'tus_sondeo' } }));
  if (resC.status !== 201) {
    return `POST /uploads devolvió ${resC.status}: ${resC.body}`;
  }
  const location = resC.headers.Location;
  if (!location) return 'la respuesta de creación no trae Location';
  const ruta = rutaTus(location);

  const trozo = bytes.slice(0, Math.min(1024, bytes.byteLength));
  const resP = http.patch(url(ruta), trozo, params({ headers: {
    Authorization: `Bearer ${prof.token}`,
    'Tus-Resumable': '1.0.0',
    'Upload-Offset': '0',
    'Content-Type': 'application/offset+octet-stream',
  }, tags: { op: 'tus_sondeo' } }));
  if (resP.status !== 204) {
    return `PATCH devolvió ${resP.status}: ${resP.body || resP.error || 'sin detalle'}`;
  }
  return null;
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

  if (!TUS_HABILITADO) {
    throw new Error('TUS_HABILITADO=false: este guion está desactivado a propósito.');
  }

  const perfil = PERFILES[0];
  const borrador = borradores[0];
  const prof = profesores[borrador.profesorIndice] || IDENTIDAD.profesores[0];
  const fallo = sondeoPrevio(perfil, CUERPOS[perfil.clave], borrador, prof);
  if (fallo) {
    throw new Error(
      'El sondeo previo de TUS falló, así que la corrida mediría un fallo de integración ' +
      'y no capacidad.\n  ' + fallo +
      '\n\nTUS contra la API S3-compatible del proveedor puede no estar soportado. ' +
      'Para medir el resto del escenario 2 sin él: TUS_HABILITADO=false.'
    );
  }

  return { adminToken: IDENTIDAD.admin.token, runId: RUN_ID, borradores, profesores, perfiles: PERFILES };
}

export function cargaTus(data) {
  const perfil = data.perfiles[exec.vu.iterationInInstance % data.perfiles.length];
  const bytes = CUERPOS[perfil.clave];
  const borrador = data.borradores[exec.vu.iterationInInstance % data.borradores.length];
  const prof = data.profesores[borrador.profesorIndice];
  if (!prof) return;

  // El recurso debe existir antes: el TUS exige resource_id en el metadata.
  const resR = json('POST', `/api/v1/units/${borrador.unidadId}/resources`,
    { type: perfil.tipo, title: `tus ${perfil.clave} ${data.runId} v${exec.vu.iterationInInstance}` },
    prof.token, 'crea_recurso');
  if (resR.status !== 201) return;
  // La respuesta viene envuelta: {"resource": {...}}. Leer el envelope entero
  // dejaba recurso.id en undefined y la ruta del upload salía como
  // /resources/undefined/upload-url.
  const recurso = (body(resR) || {}).resource;
  if (!recurso || !recurso.id) {
    throw new Error(`crear recurso devolvio una respuesta inesperada: ${resR.body}`);
  }

  const metadata = [
    `resource_id ${b64encode(String(recurso.id))}`,
    `filename ${b64encode(`${perfil.clave}.mp4`)}`,
    `filetype ${b64encode(perfil.mime)}`,
  ].join(',');

  // 1. Crear la carga.
  const t0 = Date.now();
  const resC = http.post(
    url('/api/v1/uploads'),
    null,
    params({
      headers: {
        Authorization: `Bearer ${prof.token}`,
        'Tus-Resumable': '1.0.0',
        'Upload-Length': String(bytes.byteLength),
        'Upload-Metadata': metadata,
      },
      tags: { op: 'tus_create' },
    })
  );
  latenciaCreate.add(resC.timings.duration, { perfil: perfil.clave });
  if (resC.status !== 201) {
    fallidos.add(1, { etapa: 'create', status: String(resC.status) });
    return;
  }
  creados.add(1, { perfil: perfil.clave });

  const location = resC.headers.Location;
  if (!location) {
    fallidos.add(1, { etapa: 'sin_location' });
    return;
  }
  const ruta = rutaTus(location);

  // 2. Enviar los trozos. El offset se lleva en el cliente porque PATCH lo exige.
  let offset = 0;
  while (offset < bytes.byteLength) {
    const fin = Math.min(offset + TROZO, bytes.byteLength);
    const trozo = bytes.slice(offset, fin);
    const t1 = Date.now();
    const resP = http.patch(
      url(ruta),
      trozo,
      params({
        headers: {
          Authorization: `Bearer ${prof.token}`,
          'Tus-Resumable': '1.0.0',
          'Upload-Offset': String(offset),
          'Content-Type': 'application/offset+octet-stream',
        },
        tags: { op: 'tus_patch', perfil: perfil.clave },
      })
    );
    latenciaPatch.add(resP.timings.duration, { perfil: perfil.clave });
    if (resP.status === 409) {
      // Offset inesperado: sería un defecto del guion, no de la plataforma.
      erroresOffset.add(1);
      return;
    }
    if (resP.status !== 204) {
      fallidos.add(1, { etapa: 'patch', status: String(resP.status) });
      return;
    }
    latenciaTransferencia.add(resP.timings.duration, { perfil: perfil.clave, via: 'tus' });
    throughputTransferencia.add(trozo.length, { perfil: perfil.clave, via: 'tus' });
    patches.add(1, { perfil: perfil.clave });

    const nuevo = Number(resP.headers['Upload-Offset'] || fin);
    offset = Number.isFinite(nuevo) ? nuevo : fin;
  }

  // 3. HEAD para verificar que el servidor quedó con el tamaño completo. El
  //    estado de procesamiento NO se lee aquí: lo lee el drenaje.
  const resH = http.head(
    url(ruta),
    params({
      headers: { Authorization: `Bearer ${prof.token}`, 'Tus-Resumable': '1.0.0' },
      tags: { op: 'tus_head' },
    })
  );
  const offsetFinal = Number(resH.headers['Upload-Offset'] || 0);
  if (resH.status === 200 && offsetFinal === bytes.byteLength) {
    latenciaControl.add(Date.now() - t0, { op: 'recorrido_tus' });
  } else {
    fallidos.add(1, { etapa: 'head', status: String(resH.status), offset: String(offsetFinal) });
  }
  think();
}

export function observar(data) {
  bucleObservacion(data);
}

// El header Location de TUS llega absoluto, con el host del backend. Si se le
// antepone BASE_URL a una URL absoluta se obtiene un host inválido, así que aquí
// se devuelve SOLO la ruta y es el sitio de llamada el que le pone BASE_URL con
// url(). Devolver aquí la URL completa hacía que se aplicara dos veces.
//
// El + del id (<uuid>+<multipartId>) va literal: en un path no significa espacio,
// que es lo que ocurre al decodificar una cadena de consulta.
function rutaTus(location) {
  const esquema = location.indexOf('://');
  if (esquema < 0) return location;
  return location.slice(location.indexOf('/', esquema + 3));
}

export function handleSummary(data) {
  const nivel = (__ENV && __ENV.LEVEL) || ESC2_LEVELS[0].id;
  const m = (n) => (data.metrics[n] && data.metrics[n].values) || {};
  return {
    stdout:
      `\n--- Escenario 2b · TUS · nivel ${nivel} ---\n` +
      `cargas creadas   : ${m('tus_created_total').count || 0}\n` +
      `parches enviados  : ${m('tus_patch_total').count || 0}\n` +
      `conflictos offset: ${m('tus_offset_conflict_total').count || 0}\n` +
      `fallos           : ${m('tus_failed_total').count || 0}\n` +
      `functional_error_rate: ${m('functional_error_rate').rate}\n`,
  };
}
