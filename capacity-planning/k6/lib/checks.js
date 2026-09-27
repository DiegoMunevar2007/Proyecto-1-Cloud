// Validaciones funcionales.
//
// El enunciado es explícito: un código HTTP exitoso no demuestra que el estado
// esperado se haya conservado, y una petición rechazada por datos inválidos no
// acredita carga funcional exitosa. Aquí vive la comprobación del contenido de
// cada respuesta, que es lo que separa una corrida válida de un 200 vacío.

import { check } from 'k6';
import { Counter, Rate } from 'k6/metrics';

// Integridad: cualquier valor distinto de cero es un hallazgo que hay que
// reportar, y por eso llevan umbral de abortar en los umbrales del escenario.
export const dobleCalificacion = new Counter('grades_duplicated_total');
export const intentosDuplicados = new Counter('attempts_duplicated_total');
export const respuestasInvalidas = new Counter('invalid_responses_total');
export const validadoOk = new Rate('functional_validation_rate');

export function valida(condicion, descripcion, etiquetas) {
  if (!condicion) {
    respuestasInvalidas.add(1, etiquetas || {});
  }
  validadoOk.add(condicion ? 1 : 0, etiquetas || {});
  return check(condicion, { [descripcion]: condicion });
}

// ---------------------------------------------------------------------------
// Catálogo y detalle
// ---------------------------------------------------------------------------

export function validaCatalogo(data) {
  if (!data) return valida(false, 'catalogo: cuerpo JSON interpretable');
  const bien = Array.isArray(data.courses) &&
    typeof data.total === 'number' &&
    data.page >= 1 &&
    data.limit > 0 &&
    data.courses.length <= data.limit;
  return valida(bien, 'catalogo: envelope coherente y respects limit', { op: 'catalogo' });
}

export function validaDetalle(data) {
  if (!data) return valida(false, 'detalle: cuerpo JSON interpretable');
  const course = data.course || {};
  const version = data.version || {};
  const modulos = version.modules || [];
  const recursos = [];
  (modulos || []).forEach((m) => {
    (m.units || []).forEach((u) => (u.resources || []).forEach((r) => recursos.push(r)));
  });
  const bien = typeof course.id === 'number' &&
    course.id > 0 &&
    Array.isArray(version.modules) &&
    modulos.length > 0 &&
    recursos.length > 0 &&
    // El orden de posiciones es lo que garantiza el recorrido del curso.
    modulos.every((m, i) => m.position === i + 1);
  return valida(bien, 'detalle: arbol de contenido presente y ordenado', { op: 'detalle_curso' });
}

// ---------------------------------------------------------------------------
// Inscripción
// ---------------------------------------------------------------------------

export function validaInscripcion(data) {
  if (!data) return valida(false, 'inscripcion: cuerpo JSON interpretable');
  const e = data.enrollment || {};
  return valida(typeof e.id === 'number' && e.id > 0 && typeof e.course_id === 'number',
    'inscripcion: devuelve la matricula creada', { op: 'inscripcion' });
}

// ---------------------------------------------------------------------------
// Progreso
// ---------------------------------------------------------------------------

export function validaHeartbeat(data) {
  if (!data) return valida(false, 'heartbeat: cuerpo JSON interpretable');
  return valida(
    typeof data.completed === 'boolean' &&
      typeof data.position_sec === 'number' &&
      data.position_sec >= 0,
    'heartbeat: devuelve el progreso con completed booleano',
    { op: 'heartbeat' }
  );
}

export function validaProgreso(data) {
  if (!data) return valida(false, 'progreso: cuerpo JSON interpretable');
  const estados = ['in_progress', 'completed', 'approved'];
  const bien = typeof data.total === 'number' &&
    typeof data.done === 'number' &&
    data.done <= data.total &&
    data.percent >= 0 && data.percent <= 100 &&
    estados.includes(data.status);
  return valida(bien, 'progreso: done<=total, percent en rango y estado valido', { op: 'progreso_curso' });
}

// ---------------------------------------------------------------------------
// Quiz
// ---------------------------------------------------------------------------

export function validaIntento(data) {
  if (!data) return valida(false, 'intento: cuerpo JSON interpretable');
  // Que la respuesta NO traiga correct_index es una comprobación de seguridad:
  // si aparece, es una fuga que hay que reportar en el informe.
  const sinRespuestas = !JSON.stringify(data).includes('correct_index');
  const bien = typeof data.id === 'number' && data.id > 0 &&
    ['draft', 'submitted', 'expired'].includes(data.status) &&
    Array.isArray(data.questions) &&
    sinRespuestas;
  return valida(bien, 'intento: estado valido y sin respuestas correctas filtradas', { op: 'quiz' });
}

export function validaCalificacion(data, esperada) {
  if (!data) return valida(false, 'calificacion: cuerpo JSON interpretable');
  const score = data.score;
  const bien = data.status === 'submitted' &&
    typeof score === 'number' && score >= 0 && score <= 100 &&
    (esperada === undefined || score === esperada);
  return valida(bien, 'calificacion: submitted con score entero en rango', { op: 'quiz' });
}

// ---------------------------------------------------------------------------
// Contenido y objetos
// ---------------------------------------------------------------------------

export function validaDescarga(data) {
  if (!data) return valida(false, 'descarga: cuerpo JSON interpretable');
  return valida(typeof data.url === 'string' && data.url.length > 0 && typeof data.hls === 'boolean',
    'descarga: devuelve url y hls', { op: 'autoriza_descarga' });
}

export function validaAutorizaCarga(data) {
  if (!data) return valida(false, 'autoriza_carga: cuerpo JSON interpretable');
  // task_id vacío significa que asynq descartó el encolado por duplicado: el
  // trabajo nunca se procesaría y la corrida estaría midiendo una carga perdida.
  return valida(
    typeof data.upload_url === 'string' &&
      data.upload_url.length > 0 &&
      typeof data.task_id === 'string' &&
      data.task_id.length > 0,
    'autoriza_carga: devuelve upload_url y task_id no vacio',
    { op: 'autoriza_carga' }
  );
}

// ---------------------------------------------------------------------------
// Cola
// ---------------------------------------------------------------------------

export function validaCola(data) {
  if (!data || !Array.isArray(data.queues) || data.queues.length === 0) {
    return valida(false, 'cola: envelope con al menos una cola', { op: 'cola_admin' });
  }
  const q = data.queues[0];
  return valida(
    q.queue === 'media' &&
      typeof q.pending === 'number' &&
      typeof q.active === 'number' &&
      typeof q.age_oldest_pending_sec === 'number' &&
      q.age_oldest_pending_sec >= 0,
    'cola: profundidad y antigüedad presentes',
    { op: 'cola_admin' }
  );
}
