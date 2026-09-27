# Pruebas de carga — Entrega 2

Pruebas de capacidad de la plataforma MOOC sobre el despliegue en la nube, con
**k6**. Todo se orquesta desde `Taskfile.yml`: no hay scripts de shell para la
lógica de las pruebas.

El informe con los resultados y su interpretación está en
[`pruebas_de_carga_entrega2.md`](pruebas_de_carga_entrega2.md).

## Requisitos

- `k6` >= v2 (probado con v2.3.0)
- `task` (Task, no GNU make)
- `ffmpeg` para generar los medios de prueba
- La plataforma desplegada y accesible por HTTPS

```bash
k6 version
task --version
ffmpeg -version
```

## Configuración

Las credenciales vienen del entorno, nunca del repositorio:

```bash
export BASE_URL=https://<tu-dominio>          # o http://localhost:8080 en local
export ADMIN_USER=admin1
export ADMIN_PASS='<contraseña del admin>'
export SEED_PASSWORD='<contraseña de la carga sintética>'
```

Si el certificado es autofirmado (lo normal cuando el despliegue usa la IP
pública en vez de un dominio), los guiones ya envían `insecureSkipTLSVerify`.
Para exigir verificación: `export INSECURE_TLS=false`.

## Contra el despliegue de GCP

Con `ENV=gcp` el Taskfile resuelve la URL desde Terraform, así que no hay que
copiarla ni arriesgarse a apuntar a la IP equivocada:

```bash
cd capacity-planning
export ENV=gcp ADMIN_PASS='...' SEED_PASSWORD='...'

task gcp:wait    # espera a que el arranque asíncrono de las VM termine
task gcp:seed    # siembra el corpus
task gcp:all     # escenario 1, ráfaga de login, integridad y escenario 2
```

`gcp:wait` distingue tres situaciones para no hacerte esperar sin motivo: que
Terraform no devuelva URL (despliegue sin aplicar), que el state no tenga
máquinas virtuales (despliegue a medias, con la IP reservada pero sin nada que la
atienda) y que la aplicación aún no responda.

El runbook completo está en [`scripts/IaC/README.md`](../scripts/IaC/README.md#correr-las-pruebas-de-carga-contra-el-despliegue).

### El corpus es de un entorno

Cada dataset guarda el `baseUrl` contra el que se generó. Cambiar de local a GCP
sin volver a sembrar hace que los guiones fallen con un mensaje explícito, en vez
de producir errores que parecerían defectos de la plataforma. Para regenerarlo:

```bash
task seed:reset && task seed:all
```

El seed crea el administrador si no existe, así que un despliegue recién creado
no necesita ninguna preparación manual. También es **idempotente**: si el corpus
ya está, lo reutiliza.

### Qué se puede desactivar

| Variable | Por defecto | Para qué |
|---|---|---|
| `INSECURE_TLS` | `true` | `false` cuando el despliegue usa un dominio con certificado real |
| `TUS_HABILITADO` | `true` | `false` si el proveedor no soporta TUS sobre su API S3-compatible |
| `PREMATRICULA_LOTE` | `250` | Matrículas por lote en el setup del escenario 1 |
| `LEVEL` | `L0`/`M0` | Nivel de carga a correr |

`esc2_tus.js` hace una carga TUS completa de sondeo antes de medir: si la
integración no funciona, lo dice con el código y el cuerpo de la respuesta en
lugar de dejar dos usuarios virtuales fallando durante cuatro minutos y
confundirlo con saturación.

## Orden de ejecución

```bash
task media:generate   # 1. los cuatro perfiles con ffmpeg
task seed:all         # 2. corpus sintético completo
task esc1:all         # 3. escenario 1: actividad académica
task esc1:burst       # 4. variante separada de ráfaga de login
task integridad       # 5. invariantes de idempotencia
task esc2:all         # 6. escenario 2: carga, TUS, consumo y drenaje
```

`task info` muestra la configuración con la que correría una medición.
`task check` valida que todos los guiones cargan sin ejecutarse.

### Reposición y limpieza

```bash
task seed:reset   # borra el corpus local para regenerarlo
```

Los seeds son **idempotentes**: si `datasets/*.json` ya existe, se reutiliza en
vez de volver a crearlo. Eso importa porque `courses.slug` y
`attempts.idempotency_key` tienen índice único y una corrida parcial deja
valores ocupados. Para partir de cero hay que borrar también los volúmenes del
entorno local (`docker compose down -v`).

## Qué hay en `k6/`

| Guion | Qué mide |
|---|---|
| `seed_students.js` | 4 profesores y 500 estudiantes, con sus sesiones |
| `seed_courses.js` | 20 cursos publicados de 3×3×3 recursos y 4 borradores |
| `seed_media.js` | Sube los 4 perfiles y espera a que queden `available` |
| `esc1_academico.js` | Escenario 1: catálogo, curso, progreso, inscripción, heartbeat, quiz |
| `esc1_login_burst.js` | Variante separada de ráfaga de inicios de sesión |
| `esc2_carga_directa.js` | Escenario 2a: carga directa con URL prefirmada |
| `esc2_tus.js` | Escenario 2b: carga TUS, cuyos bytes atraviesan la API |
| `esc2_consumo.js` | Escenario 2c: consumo HLS a cadencia de reproducción |
| `esc2_drenaje.js` | Escenario 2d: drena la cola y verifica el estado final |
| `integridad.js` | Idempotencia, doble calificación y progreso manipulado |

En `k6/lib/` está lo compartido: `config.js` (niveles y parámetros),
`api.js` (cliente HTTP y clasificación de respuestas), `checks.js`
(validaciones de contenido), `corpus.js` (carga del corpus) y
`observacion.js` (sondeo de la cola).

## Decisiones que conviene conocer antes de leer los resultados

**El login no está en el recorrido medido.** El escenario 1 usa sesiones
preparadas en el seed. El motivo es concreto: `login` verifica bcrypt en el
request path, y de estarlo falsearía el cuello de botella. La ráfaga de login se
mide aparte, en `esc1_login_burst.js`, y se reporta como variante.

**Cada estudiante tiene su propio conjunto de cursos matriculados.** Si todos
compartieran curso, un solo par (estudiante, quiz) agotaría sus tres intentos en
pocas iteraciones y el resto de la corrida mediría `409` en lugar de calificación.
Con 5 cursos por estudiante hay 45 quizzes disponibles, es decir 135 intentos.

**Un `200` no es carga funcional exitosa.** Cada respuesta se valida por
contenido: el catálogo declara un sobre coherente, el heartbeat devuelve
`completed` booleano, el intento nunca incluye `correct_index`, la calificación
está en rango. Un intento que devuelve `correct_index` se contaría como fuga.

**Lo que se considera rechazo esperado** está separado de lo que se considera
fallo: `409` de intentos agotados, `403` sin matrícula y `404` de recurso sin
archivo son reglas de negocio. Solo los `5xx` y los `4xx` inesperados cuentan
como error funcional, y solo ese dispara el umbral de aborto.

**El tiempo hasta el primer cuadro no se reporta.** El enunciado pide que no se
atribuya a peticiones HTTP, y no se usa un reproductor.

**La cadencia de reproducción sale del manifiesto, no de una suposición.** El
worker pasa `-hls_time 6`, pero ffmpeg corta en el siguiente keyframe: medido en
este corpus, el video produce segmentos de ~10,4 s y el audio de ~6,0 s. El guion
lee la duración de cada manifiesto.

## Resultados

Cada corrida escribe en `results/`:

- `<escenario>-<nivel>.json`: una línea por muestra de k6, con la latencia de
  cada endpoint etiquetada por operación.
- `<escenario>-<nivel>-summary.json`: el resumen de la corrida.

El Taskfile imprime además un resumen corto por corrida. Los gráficos salen de
los archivos JSON; se dejan aparte a propósito porque el generador de graphs no
debe mezclarse con la generación de la carga.
