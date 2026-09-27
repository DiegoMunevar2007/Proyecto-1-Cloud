// Seed multimedia: sube los archivos de prueba y espera a que estén available.
//
// Los perfiles se generan con `task media:generate` (ffmpeg local) y se leen desde
// `media/`. La subida usa la URL prefirmada, es decir carga directa al
// almacenamiento de objetos: los bytes NO pasan por la API, que es exactamente
// el patrón que el escenario 2 quiere medir.
//
// Rendiciones: el worker aplica `scale=w=-2:h='min(ih,720)'`, así que hay UNA
// sola rendición con tope de 720p y sin upscale: un original de 1080p produce
// 720p.
//
// El número de segmentos NO se supone: se cuenta en el manifiesto tras el
// procesamiento. El worker pasa `-hls_time 6`, pero ffmpeg corta el segmento en
// el siguiente keyframe y, sin forzar el intervalo de keyframes, el GOP por
// defecto no está alineado con los 6 s. Medido en este corpus, los segmentos
// duran del orden de 10 s, de modo que un original de 30 s da 3 segmentos y no
// los 5 que se obtendrían con `-hls_time 6` exacto. Declarar una cantidad
// calculada en lugar de medida haría que el informe afirmara una estructura HLS
// que el sistema no produce.
//
// Todo ocurre en setup() a propósito: el trabajo asíncrono tarda de segundos a
// minutos y es un drenado, no una latencia de request. Aquí no se mide nada.

import http from 'k6/http';
import { sleep } from 'k6';
import { Counter, Trend } from 'k6/metrics';
import { params, url } from './lib/api.js';
import { abrirDataset, abrirMedio, corpusReutilizable } from './lib/corpus.js';

export const options = { vus: 1, iterations: 1, setupTimeout: '40m' };

// open() solo existe en el contexto de init.
const IDENTIDAD = abrirDataset('estudiantes.json');
const CONTENIDO = abrirDataset('cursos.json');
// open() solo existe en init, así que la guarda se evalúa al cargar el módulo.
// Un media.json de otro entorno no se reutiliza: sus hlsUrl apuntan a otro bucket.
const MEDIA_EXISTE = corpusReutilizable('media.json');

// Los archivos se leen en el contexto de init, que es el único donde open()
// existe. Este guion corre con un solo VU, así que mantener los cuatro perfiles
// en memoria es aceptable: p3-large es el mayor, del orden de 20 MB.
//
// La ruta es relativa al guion de entrada, que vive en k6/, de modo que
// ../media resuelve en capacity-planning/media.
const PERFILES = [
  { clave: 'p1-small', archivo: 'p1-small.mp4', mime: 'video/mp4', tipo: 'video', extension: 'mp4', resolucion: '854x480', segundos: 30, segmentos: 5 },
  { clave: 'p2-medium', archivo: 'p2-medium.mp4', mime: 'video/mp4', tipo: 'video', extension: 'mp4', resolucion: '1280x720', segundos: 60, segmentos: 10 },
  { clave: 'p3-large', archivo: 'p3-large.mp4', mime: 'video/mp4', tipo: 'video', extension: 'mp4', resolucion: '1920x1080', segundos: 120, segmentos: 20 },
  { clave: 'a1-audio', archivo: 'a1-audio.m4a', mime: 'audio/mp4', tipo: 'audio', extension: 'm4a', resolucion: 'audio', segundos: 180, segmentos: 30 },
];

const CUERPOS = {};
for (const p of PERFILES) {
  CUERPOS[p.clave] = abrirMedio(p.archivo);
}

const subida = new Trend('seed_upload_ms');
const endToEnd = new Trend('seed_time_to_available_ms');
const listos = new Counter('seed_media_ready_total');
const fallidos = new Counter('seed_media_failed_total');

function post(path, body, token, op) {
  return http.post(
    url(path),
    JSON.stringify(body),
    params({
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
      tags: { op },
    })
  );
}

function get(path, token, op) {
  return http.get(
    url(path),
    params({ headers: token ? { Authorization: `Bearer ${token}` } : {}, tags: { op } })
  );
}

// Busca un recurso por id en el árbol que devuelve el detalle del curso.
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

export function setup() {
  // Idempotente: los objetos multimedia son únicos por clave y la cola no
  // admite repetir la tarea, así que subir dos veces lo mismo no aporta nada.
  if (MEDIA_EXISTE) {
    return { reutilizado: true };
  }
  const cursos = CONTENIDO;
  const identidad = IDENTIDAD;
  if (!cursos.borradores || !cursos.borradores.length) {
    throw new Error('datasets/cursos.json no tiene borradores. Corre antes: task seed:courses');
  }

  const esperaMaxMs = Number(__ENV.MEDIA_WAIT_MIN || 20) * 60 * 1000;
  const subidos = [];

  // Curso propio para el multimedia consumible. No se reutilizan los borradores
  // de seed_courses porque al terminar they'd quedar en borrador, y un estudiante
  // solo puede consumir contenido de un curso publicado y matriculado. Y un
  // curso publicado ya no admite recursos nuevos, que es justo lo que necesita el
  // escenario 2a. Son dos necesidades distintas y por eso dos cursos.
  const prof = identidad.profesores[0];
  const slugMedia = `${cursos.runId}-m`;
  const resC = post('/api/v1/courses',
    { title: `Medios ${slugMedia}`, slug: slugMedia,
      description: 'Curso de medios para el escenario de consumo.', min_required_pct: 80 },
    prof.token, 'seed_media_curso');
  if (resC.status !== 201) {
    throw new Error(`crear curso de medios: ${resC.status} ${resC.body}`);
  }
  const cursoMedioId = resC.json().course.id;
  const resMod = post(`/api/v1/courses/${cursoMedioId}/modules`,
    { title: 'Módulo de medios', description: 'Perfiles multimedia.' },
    prof.token, 'seed_media_modulo');
  const moduloMedioId = resMod.json().module.id;
  const resUni = post(`/api/v1/modules/${moduloMedioId}/units`,
    { title: 'Unidad de medios', description: 'Perfiles multimedia.' },
    prof.token, 'seed_media_unidad');
  const unidadMedioId = resUni.json().unit.id;

  for (let i = 0; i < PERFILES.length; i += 1) {
    const p = PERFILES[i];

    const resR = post(`/api/v1/units/${unidadMedioId}/resources`,
      { type: p.tipo, title: `${p.clave} ${cursos.runId}` }, prof.token, 'seed_media_recurso');
    if (resR.status !== 201) {
      throw new Error(`crear recurso ${p.clave}: ${resR.status} ${resR.body}`);
    }
    const recurso = resR.json().resource;

    // La clave del objeto lleva el milisegundo de encolado. Es lo que permite
    // medir time-to-available sin estado compartido entre VU: el escenario de
    // drenaje parsea la misma clave y lo resta del instante en que ve ready.
    const encoladoEnMs = Date.now();
    const objectKey = `k6/${cursos.runId}/${p.clave}-${recurso.stable_id}-${encoladoEnMs}.${p.extension}`;

    const bytes = CUERPOS[p.clave];
    const resU = post(`/api/v1/resources/${recurso.id}/upload-url`,
      { object_key: objectKey, mime_type: p.mime, size_bytes: bytes.byteLength },
      prof.token, 'autoriza_carga');
    if (resU.status !== 200) {
      throw new Error(`upload-url ${p.clave}: ${resU.status} ${resU.body}`);
    }
    const autorizado = resU.json();
    if (!autorizado.task_id) {
      // task_id vacío = asynq descartó el encolado por duplicado. El trabajo no
      // se procesaría y estaríamos contando una carga perdida.
      throw new Error(`task_id vacío para ${p.clave}: el encolado fue descartado`);
    }

    const t0 = Date.now();
    const resP = http.put(autorizado.upload_url, bytes, params({ tags: { op: 'transferencia_objeto' } }));
    subida.add(Date.now() - t0, { perfil: p.clave });
    if (resP.status < 200 || resP.status >= 300) {
      throw new Error(`PUT al almacenamiento ${p.clave}: ${resP.status} ${resP.body}`);
    }

    subidos.push({
      perfil: p.clave, tipo: p.tipo, mime: p.mime,
      resolucionOriginal: p.resolucion, segundos: p.segundos,
      segmentosEsperados: p.segmentos,
      resourceId: recurso.id, stableId: recurso.stable_id, objectKey,
      cursoMedioId, profesorIndice: prof.indice,
      profesorToken: prof.token, encoladoEnMs, taskId: autorizado.task_id,
      processing_status: 'pending', hlsUrl: null,
    });
  }

  // Drenado: espera a que cada recurso llegue a ready. El sondeo es de pocos
  // segundos contra un trabajo de minutos, así que no perturba nada.
  for (const s of subidos) {
    const t0 = Date.now();
    let resuelto = false;
    while (Date.now() - t0 < esperaMaxMs) {
      const res = get(`/api/v1/courses/${s.cursoMedioId}`, s.profesorToken, 'drenaje');
      const r = recursoEnArbol(res.json() || {}, s.resourceId);
      if (r) {
        s.processing_status = r.processing_status;
        s.scan_status = r.scan_status;
        s.hls_key = r.hls_key;
        if (r.processing_status === 'ready') {
          // La URL pública del manifiesto la entrega la API, que es la que
          // autoriza el consumo. k6 no necesita conocer S3_PUBLIC_ENDPOINT.
          const resD = get(`/api/v1/resources/${s.resourceId}/download-url`, s.profesorToken, 'autoriza_descarga');
          const d = resD.json() || {};
          if (d.hls === true && typeof d.url === 'string' && d.url) {
            s.hlsUrl = d.url;
            // Se cuenta el manifiesto real en lugar de suponerlo.
            try {
              const raw = http.get(d.url,
                params({ tags: { op: 'manifiesto_hls' } })).body || '';
              const n = (raw.match(/#EXTINF/g) || []).length;
              const dur = (raw.match(/#EXTINF:([0-9.]+)/) || [])[1];
              s.segmentosObservados = n;
              s.duracionSegmentoSeg = dur ? Number(dur) : null;
            } catch (_e) {
              s.segmentosObservados = null;
            }
          }
          endToEnd.add(Date.now() - s.encoladoEnMs, { perfil: s.perfil });
          listos.add(1, { perfil: s.perfil });
          resuelto = true;
          break;
        }
        if (r.processing_status === 'failed') {
          break;
        }
      }
      sleep(10);
    }
    if (!resuelto) {
      fallidos.add(1, { perfil: s.perfil });
      throw new Error(
        `el recurso ${s.perfil} (id ${s.resourceId}) no llegó a ready en ` +
        `${__ENV.MEDIA_WAIT_MIN || 20} min; processing_status=${s.processing_status}. ` +
        'Revisa los logs del worker y la cola antes de medir.'
      );
    }
  }

  // Publicar: la validación exige que todo recurso multimedia visible esté con
  // scan limpio y processing ready, que es exactamente lo que se acaba de
  // comprobar. Si aun así falla, el mensaje dice por qué.
  const resVal = get(`/api/v1/courses/${cursoMedioId}/validate`, prof.token, 'seed_media_valida');
  const val = resVal.json() || {};
  if (!val.valid) {
    throw new Error(`el curso de medios no es publicable: ${JSON.stringify(val.errors)}`);
  }
  const resPub = post(`/api/v1/courses/${cursoMedioId}/publish`, {}, prof.token, 'seed_media_publica');
  if (resPub.status !== 200) {
    throw new Error(`publicar el curso de medios: ${resPub.status} ${resPub.body}`);
  }

  return {
    runId: cursos.runId,
    entorno: ENTORNO,
    baseUrl: BASE_URL,
    generadoEn: new Date().toISOString(),
    cursoMedioId,
    publicado: true,
    subidos: subidos.map((s) => ({
      perfil: s.perfil, tipo: s.tipo, mime: s.mime,
      resolucionOriginal: s.resolucionOriginal, segundos: s.segundos,
      segmentosObservados: s.segmentosObservados,
      duracionSegmentoSeg: s.duracionSegmentoSeg,
      resourceId: s.resourceId, stableId: s.stableId, objectKey: s.objectKey,
      cursoMedioId: s.cursoMedioId, profesorIndice: s.profesorIndice,
      encoladoEnMs: s.encoladoEnMs, taskId: s.taskId,
      processing_status: s.processing_status, hlsUrl: s.hlsUrl,
    })),
  };
}

export default function () {}

export function handleSummary(data) {
  const corpus = data.setup_data || {};
  if (corpus.reutilizado) {
    return { stdout: '\ndatasets/media.json ya existe: se reutiliza.\n' };
  }
  // handleSummary se ejecuta incluso cuando setup() falla. Escribir ahí el corpus
  // vacío dejaría un datasets/*.json corrupto que la corrida siguiente leería
  // como válido, así que solo se escribe si el seed realmente produjo datos.
  if (!corpus || Object.keys(corpus).length === 0) {
    return { stdout: '\nAVISO: el seed no produjo datos; no se escribe el corpus.\n' };
  }
  return {
    'datasets/media.json': JSON.stringify(corpus, null, 2),
    stdout: `\nseed media: ${(corpus.subidos || []).length} archivos listos\n`,
  };
}
