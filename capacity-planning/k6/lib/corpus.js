// Carga del corpus sembrado y utilidades de derivación.
//
// El corpus lo producen los scripts de seed y queda en `datasets/`. Cada
// corrida de medición lo consume de solo lectura, de modo que sembrar no sesga
// la medición y los datos sintéticos se declaran una vez en el informe.
//
// Rutas. k6 resuelve open() e import.meta.resolve() respecto al guion de entrada,
// que en este proyecto está siempre en k6/, así que ../datasets/ es la ruta
// correcta. Pero k6 advierte que esa resolución puede cambiar para que sea
// relativa al módulo que escribe la llamada, en cuyo caso haría falta
// ../../datasets/ (este módulo vive en k6/lib). En vez de depender de una de las
// dos, se prueban en orden: la primera que abre gana. Así el guion funciona en
// las dos semánticas sin tener que tocarlo cuando k6 cambie.

import { BASE_URL } from './config.js';

// El corpus se generó contra la URL pública del despliegue, pero una corrida
// desde dentro de la VPC apunta a la IP privada de la misma máquina para no
// medir el enrutado al borde de Google. CORPUS_BASE_URL declara contra qué URL
// se generó el corpus cuando el objetivo de la corrida es otro; sin esa
// variable se asume que coinciden, que es el caso de una corrida desde fuera.
const BASE_ESPERADA = __ENV.CORPUS_BASE_URL || BASE_URL;

const PREFIJOS = ['../', '../../'];

export function abrirDataset(nombre) {
  const crudo = abrirConCandidatos(nombre, 'datasets/');
  let datos;
  try {
    datos = JSON.parse(crudo);
  } catch (_e) {
    throw new Error(
      `${nombre} no es JSON válido. Suele significar que un seed anterior falló y ` +
      'dejó el corpus vacío: bórralo y vuelve a correr el seed.'
    );
  }
  verificarEntorno(nombre, datos);
  return datos;
}

// Un corpus generado contra un entorno no sirve en otro: los tokens de sesión
// apuntan a usuarios de la otra base y los ids de curso y de recurso no existen.
// Sin esta comprobación, cambiar de local a GCP produce fallos que parecen
// defectos de la plataforma cuando en realidad es el corpus equivocado.
//
// Un corpus sin `baseUrl` tampoco se acepta: no se puede atribuir a ningún
// entorno, y aceptarlo es justo el agujero por el que un corpus de otra base
// pasa por bueno en silencio. Todos los seeds escriben `baseUrl`, así que su
// ausencia significa que el archivo viene de otro lado.
function verificarEntorno(nombre, datos) {
  if (!datos || !datos.baseUrl) {
    throw new Error(
      `${nombre} no declara baseUrl, así que no se puede saber contra qué entorno ` +
      'se generó. Regenera el corpus con: task seed:reset && task seed:all.'
    );
  }
  if (datos.baseUrl === BASE_ESPERADA) return;
  throw new Error(
    `${nombre} se generó contra ${datos.baseUrl}, pero se esperaba ${BASE_ESPERADA}` +
    ` (objetivo de la corrida: ${BASE_URL}).` +
    ` El corpus es de otro entorno` +
    (datos.entorno ? ` (${datos.entorno})` : '') +
    '.\nPara regenerarlo: task seed:reset && task seed:all (con el BASE_URL de este entorno).'
  );
}

// Lee un medio de prueba en binario. El guion que llama tiene que estar en el
// contexto de init, que es el único donde existe open().
export function abrirMedio(nombre) {
  return abrirConCandidatos(nombre, 'media/', 'b');
}

// Dice si un archivo existe, sin lanzar.
export function existeDataset(nombre) {
  for (const prefijo of PREFIJOS) {
    try {
      open(rutaAbsoluta(prefijo + 'datasets/' + nombre));
      return true;
    } catch (_e) {
      // Se prueba el siguiente candidato.
    }
  }
  return false;
}

// Dice si el corpus se puede reutilizar tal cual: tiene que existir y tener que
// haber sido generado contra este mismo entorno. La existencia sola no alcanza:
// un corpus de local en una corrida contra GCP hace fallar cada matriculación con
// tokens que no existen en la base de la nube, y el seed se lo calla.
//
// Los seeds usan esto en vez de existeDataset porque sembrar es lento y caro
// (bcrypt por usuario), pero reused un corpus equivocado es más caro todavía.
export function corpusReutilizable(nombre) {
  if (!existeDataset(nombre)) return false;
  try {
    abrirDataset(nombre);
    return true;
  } catch (_e) {
    // No es reutilizable: o es de otro entorno o no declara dónde se generó.
    return false;
  }
}

function abrirConCandidatos(nombre, carpeta, modo) {
  const probadas = [];
  const errores = [];
  for (const prefijo of PREFIJOS) {
    const ruta = rutaAbsoluta(prefijo + carpeta + nombre);
    probadas.push(ruta);
    try {
      return open(ruta, modo);
    } catch (e) {
      // El error se guarda y se sigue: puede ser simplemente que esta ruta no
      // es la válida. Al final se reportan todos, para no enmascarar la causa
      // real (por ejemplo, invocar open() fuera del contexto de init).
      errores.push(String(e && e.message ? e.message : e));
    }
  }
  throw new Error(
    `No se pudo abrir ${carpeta}${nombre}. Se probaron:\n  ${probadas.join('\n  ')}\n` +
    `Errores: ${errores.join(' | ')}\n` +
    'Genera el corpus antes con: task seed:students (o seed:courses / seed:media).'
  );
}

function rutaAbsoluta(relativa) {
  // import.meta.resolve devuelve un file://; k6 no tiene el global URL, así que
  // el esquema se quita a mano.
  return decodeURIComponent(import.meta.resolve(relativa).replace(/^file:\/\//, ''));
}

// Elige un elemento al azar. El Math.random de k6 no está sembrado, así que las
// elecciones se reintentan con todos los VU: es aceptable para un generador de
// carga, pero el reparto de coursework por estudiante es determinista a
// propósito (ver `estudianteDe`).
export function elige(lista) {
  return lista[Math.floor(Math.random() * lista.length)];
}

// Un VU siempre trabaja con el mismo estudiante. El enunciado pide cuentas
// distintas para evitar conflictos artificiales entre usuarios virtuales, y una
// asignación fija por VU lo garantiza entre corridas: dos VUs nunca pelean por
// el mismo intento ni por el mismo progreso.
export function estudianteDe(corpus, vu) {
  return corpus.estudiantes[vu % corpus.estudiantes.length];
}

export function profesorDe(corpus, indice) {
  return corpus.profesores[indice % corpus.profesores.length];
}

// Extrae el millisegundo de encolado que el escenario de carga escribe en la
// clave del objeto. Es lo que permite medir time-to-available sin estado
// compartido entre VU: el que observa el recurso parsea la misma clave.
export function epochDeClave(objectKey) {
  const m = /-(\d{13})\.[a-z0-9]+$/.exec(objectKey || '');
  return m ? Number(m[1]) : null;
}
