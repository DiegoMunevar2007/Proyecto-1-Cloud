// Seed del contenido académico: cursos, módulos, unidades, recursos y quizzes.
//
// Todos los cursos quedan publicados y contienen solo recursos que no dependen
// del pipeline multimedia (text, pdf y quiz). Es deliberado: el escenario 1 mide
// actividad académica y no debe quedar acoplado a la transcodificación, que
// pertenece al escenario 2. Cada curso lleva además un borrador con una unidad
// libre, porque AddResource exige una versión editable y en un curso publicado
// no se pueden agregar recursos.

import http from 'k6/http';
import { sleep } from 'k6';
import { Counter, Trend } from 'k6/metrics';
import { BASE_URL, COURSE_COUNT, COURSE_SHAPE, ENTORNO, RUN_ID } from './lib/config.js';
import { params, url } from './lib/api.js';
import { abrirDataset, corpusReutilizable } from './lib/corpus.js';

export const options = { vus: 1, iterations: 1, setupTimeout: '20m' };

// open() solo existe en el contexto de init, así que el corpus se lee al
// cargar el módulo y no dentro de setup().
// Este seed produce datasets/cursos.json, así que solo lee el de identidad.
const IDENTIDAD = abrirDataset('estudiantes.json');

// courses.slug tiene índice único: si el corpus ya existe y es de este entorno,
// recrearlo falla. Si es de otro, se recrea aunque el archivo esté.
const CURSOS_EXISTEN = corpusReutilizable('cursos.json');

const porCurso = new Trend('seed_course_total_ms');
const publicaciones = new Counter('seed_courses_published_total');

// Un curso de ejemplo es siempre más grande que un curso real solo de texto,
// así que el PDF se modela como recurso externo y el quiz con markdown de
// instrucciones: ambos satisfacen la validación de publicación.
const PREGUNTAS = [
  {
    prompt: '¿Qué es una variable?',
    choices: ['Un tipo', 'Un valor con nombre', 'Una función', 'Un módulo'],
    correct_index: 1,
  },
  {
    prompt: '¿Go es un lenguaje compilado?',
    choices: ['Sí', 'No'],
    correct_index: 0,
  },
  {
    prompt: '¿Qué componente guarda la cola de trabajos?',
    choices: ['Redis', 'PostgreSQL', 'Cloud Storage', 'Caddy'],
    correct_index: 0,
  },
];

function crearCurso(token, titulo, slug) {
  return http.post(
    url('/api/v1/courses'),
    JSON.stringify({
      title: titulo,
      slug,
      description: `Curso sintético de carga ${slug}. Contenido generado para el análisis de capacidad.`,
      min_required_pct: COURSE_SHAPE.minRequiredPct,
    }),
    params({
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
      tags: { op: 'seed_curso' },
    })
  );
}

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

function cursoCompleto(token, indice) {
  const slug = `${RUN_ID}-c${indice}`;
  const res = crearCurso(token, `Curso ${slug}`, slug);
  if (res.status === 500 && /duplicate|unique/i.test(res.body || '')) {
    throw new Error(
      `El slug "${slug}" ya existe. courses.slug tiene índice único: usa otro RUN_ID para esta corrida.`
    );
  }
  if (res.status !== 201) {
    throw new Error(`crear curso ${slug}: ${res.status} ${res.body}`);
  }
  const courseId = res.json().course.id;

  const modulos = [];
  const unidades = [];
  const recursos = [];
  let quizzesPendientes = [];

  for (let m = 0; m < COURSE_SHAPE.modules; m += 1) {
    const resM = post(`/api/v1/courses/${courseId}/modules`,
      { title: `Módulo ${m + 1}`, description: `Módulo ${m + 1} del curso ${slug}.` },
      token, 'seed_modulo');
    if (resM.status !== 201) {
      throw new Error(`crear módulo ${m} de ${slug}: ${resM.status} ${resM.body}`);
    }
    const modulo = resM.json().module;
    modulos.push({ id: modulo.id, stableId: modulo.stable_id });

    for (let u = 0; u < COURSE_SHAPE.unitsPerModule; u += 1) {
      const resU = post(`/api/v1/modules/${modulo.id}/units`,
        { title: `Unidad ${m + 1}.${u + 1}`, description: `Contenido de la unidad ${m + 1}.${u + 1}.` },
        token, 'seed_unidad');
      if (resU.status !== 201) {
        throw new Error(`crear unidad ${u} de ${slug}: ${resU.status} ${resU.body}`);
      }
      const unidad = resU.json().unit;
      unidades.push({ id: unidad.id, stableId: unidad.stable_id });

      for (const tipo of COURSE_SHAPE.resourcesPerUnit) {
        const payload = recursoPorTipo(tipo, m, u, slug);
        const resR = post(`/api/v1/units/${unidad.id}/resources`, payload, token, 'seed_recurso');
        if (resR.status !== 201) {
          throw new Error(`crear recurso ${tipo}: ${resR.status} ${resR.body}`);
        }
        const recurso = resR.json().resource;
        recursos.push({
          id: recurso.id,
          stableId: recurso.stable_id,
          type: recurso.type,
          title: recurso.title,
          modulo: modulo.id,
          unidad: unidad.id,
        });
        if (tipo === 'quiz') quizzesPendientes.push(recurso.id);
      }
    }
  }

  // Los quizzes se crean después de tener todos los recursos.
  const quizzes = [];
  for (const resourceId of quizzesPendientes) {
    const resQ = post('/api/v1/quizzes',
      { resource_id: resourceId, attempts_allowed: 3, feedback_policy: 'on_submit' },
      token, 'seed_quiz');
    if (resQ.status !== 201) {
      throw new Error(`crear quiz para recurso ${resourceId}: ${resQ.status} ${resQ.body}`);
    }
    const quizId = resQ.json().quiz ? resQ.json().quiz.id : resQ.json().id;
    const resP = post(`/api/v1/quizzes/${quizId}/questions`,
      { questions: PREGUNTAS }, token, 'seed_preguntas');
    if (resP.status !== 201) {
      throw new Error(`agregar preguntas al quiz ${quizId}: ${resP.status} ${resP.body}`);
    }
    quizzes.push({ id: quizId, resourceId, correctas: PREGUNTAS.map((p) => p.correct_index) });
  }

  // La publicación es la validación real del curso: si algo del árbol no cumple,
  // devuelve 400 con la lista de errores y es mejor fallar aquí que medir un
  // catálogo con cursos que no se pueden matricular.
  const resV = http.get(
    url(`/api/v1/courses/${courseId}/validate`),
    params({ headers: { Authorization: `Bearer ${token}` }, tags: { op: 'seed_valida' } })
  );
  const validacion = (resV.json() || {}).valid;
  if (!validacion) {
    throw new Error(`curso ${slug} no publicable: ${JSON.stringify(resV.json())}`);
  }

  const resPub = post(`/api/v1/courses/${courseId}/publish`, {}, token, 'seed_publica');
  if (resPub.status !== 200) {
    throw new Error(`publicar ${slug}: ${resPub.status} ${resPub.body}`);
  }
  publicaciones.add(1);

  return { indice, id: courseId, titulo: `Curso ${slug}`, slug, modulos, unidades, recursos, quizzes };
}

function recursoPorTipo(tipo, m, u, slug) {
  const base = { title: `${tipo} ${m + 1}.${u + 1}` };
  if (tipo === 'text') {
    // text exige markdown_body en la validación de entrada.
    return Object.assign(base, {
      type: 'text',
      markdown_body: `## ${tipo} ${m + 1}.${u + 1}\n\nContenido sintético del curso ${slug}.`,
    });
  }
  if (tipo === 'pdf') {
    // pdf no exige contenido al crearse, pero sí para publicar.
    return Object.assign(base, { type: 'pdf', external_url: 'https://example.invalid/apuntes.pdf' });
  }
  // quiz cae en la rama por defecto de la validación de publicación, que exige
  // object_key, external_url o markdown_body: se le da markdown_body.
  return Object.assign(base, {
    type: 'quiz',
    markdown_body: 'Responde el quiz para completar la unidad.',
  });
}

// Un curso en borrador por profesor, con una unidad donde el escenario 2 pueda
// crear recursos. AddResource exige versión editable, así que este borrador es
// imprescindible para las cargas de archivo.
function borradorDeProfesor(profesor, indice) {
  const slug = `${RUN_ID}-b${profesor.indice}`;
  const res = crearCurso(profesor.token, `Borrador ${slug}`, slug);
  if (res.status === 500 && /duplicate|unique/i.test(res.body || '')) {
    throw new Error(`El slug "${slug}" ya existe: usa otro RUN_ID.`);
  }
  if (res.status !== 201) {
    throw new Error(`crear borrador ${slug}: ${res.status} ${res.body}`);
  }
  // El nombre de la clave del corpus es cursoId, y es el que leen los demás
  // guiones, así que la variable local se llama igual para que no se desincronicen.
  const cursoId = res.json().course.id;
  const resM = post(`/api/v1/courses/${cursoId}/modules`,
    { title: 'Módulo de carga', description: 'Unidad para cargar multimedia.' },
    profesor.token, 'seed_borrador_modulo');
  const moduloId = resM.json().module.id;
  const resU = post(`/api/v1/modules/${moduloId}/units`,
    { title: 'Unidad de carga', description: 'Unidad para cargar multimedia.' },
    profesor.token, 'seed_borrador_unidad');
  const unidad = resU.json().unit;
  return {
    profesorIndice: profesor.indice,
    profesor: profesor.username,
    cursoId,
    moduloId,
    unidadId: unidad.id,
    slug,
  };
}

export function setup() {
  // Idempotente: courses.slug tiene índice único, así que recrear los cursos con
  // el mismo RUN_ID falla. Si el corpus existe, se reutiliza.
  if (CURSOS_EXISTEN) {
    return { reutilizado: true };
  }
  const identidad = IDENTIDAD;
  if (!identidad || !identidad.profesores || !identidad.profesores.length) {
    throw new Error('datasets/estudiantes.json no tiene profesores. Corre primero: task seed:students');
  }

  const borradores = identidad.profesores.map((p) => borradorDeProfesor(p, 0));

  // Los cursos se reparten entre los profesores. Cada curso es una cadena
  // secuencial de unas 43 llamadas, así que la concurrencia está en los cursos
  // (uno por profesor y por tanda), no dentro de cada curso.
  const cursos = [];
  for (let i = 0; i < COURSE_COUNT; i += 1) {
    const prof = identidad.profesores[i % identidad.profesores.length];
    const t0 = Date.now();
    const curso = cursoCompleto(prof.token, i);
    porCurso.add(Date.now() - t0, { profesor: String(prof.indice) });
    cursos.push(curso);
    sleep(0.05);
  }

  const totalRecursos = cursos.reduce((acc, c) => acc + c.recursos.length, 0);
  return {
    runId: RUN_ID,
    entorno: ENTORNO,
    baseUrl: BASE_URL,
    generadoEn: new Date().toISOString(),
    forma: COURSE_SHAPE,
    cursos,
    borradores,
    totales: {
      cursos: cursos.length,
      modulos: cursos.reduce((a, c) => a + c.modulos.length, 0),
      unidades: cursos.reduce((a, c) => a + c.unidades.length, 0),
      recursos: totalRecursos,
      quizzes: cursos.reduce((a, c) => a + c.quizzes.length, 0),
    },
  };
}

export default function () {}

export function handleSummary(data) {
  const corpus = data.setup_data || {};
  if (corpus.reutilizado) {
    return { stdout: '\ndatasets/cursos.json ya existe: se reutiliza.\n' };
  }
  // handleSummary se ejecuta incluso cuando setup() falla. Escribir ahí el corpus
  // vacío dejaría un datasets/*.json corrupto que la corrida siguiente leería
  // como válido, así que solo se escribe si el seed realmente produjo datos.
  if (!corpus || Object.keys(corpus).length === 0) {
    return { stdout: '\nAVISO: el seed no produjo datos; no se escribe el corpus.\n' };
  }
  return {
    'datasets/cursos.json': JSON.stringify(corpus, null, 2),
    stdout:
      `\nseed contenido: cursos=${corpus.totales ? corpus.totales.cursos : 0} ` +
      `recursos=${corpus.totales ? corpus.totales.recursos : 0} ` +
      `quizzes=${corpus.totales ? corpus.totales.quizzes : 0} ` +
      `borradores=${corpus.borradores ? corpus.borradores.length : 0}\n` +
      `corpus escrito en datasets/cursos.json\n`,
  };
}
