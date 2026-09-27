# Arquitectura desplegada (Entrega 2)

Plataforma MOOC sobre Google Cloud. Este documento describe la solución que quedó efectivamente desplegada y los cambios frente a la entrega anterior. Está pensado para que otra persona del equipo pueda entender qué se construyó, por qué se tomaron ciertas decisiones y qué habría que tocar para llevarlo a la arquitectura objetivo.

Los dos diagramas están en `docs/entrega2/diagramas/`. El de componentes muestra las piezas de la aplicación y cómo se hablan entre ellas. El de despliegue muestra en qué máquina vive cada pieza y qué reglas de red las separan.

## Componentes

El usuario entra siempre por Caddy, que es el único punto público de la plataforma. Caddy termina el TLS y hace de proxy inverso, de modo que la API nunca queda expuesta directamente a Internet. Detrás de Caddy está la API, un monolito modular escrito en Go.

La decisión de mantener un monolito modular y no separar en microservicios es la misma de la entrega anterior, y el enunciado la respalda cuando dice que la separación en máquinas corresponde al modelo de despliegue y no exige transformar los módulos de negocio. Los módulos son `auth`, `admin`, `courses`, `enroll`, `quiz`, `progress`, `badges`, `uploads`, `storage`, `mail`, `queue` y `telemetry`. Cada uno registra su grupo de rutas sobre el mismo enrutador y comparte la conexión a la base de datos, el cliente de Redis y el cliente de almacenamiento. No hay llamadas por red entre ellos, porque viven en un solo proceso.

El módulo `courses` merece una mención aparte porque concentra la lógica de autoría y de carga. Es el que registra la jerarquía de curso, módulo, unidad y recurso, y también el que emite las URLs prefirmadas de subida y de descarga. Los módulos `quiz` y `progress` son los que más escriben en la base de datos durante el escenario académico.

La base de datos es Cloud SQL para PostgreSQL. Guarda las cuentas, la estructura académica, los recursos y sus metadatos, las inscripciones, los intentos, las calificaciones, el progreso y los trabajos de multimedia. La API la consulta por SQL sobre la IP privada, y las reglas de red limitan el acceso a las máquinas que la necesitan.

La cola de mensajería es Redis con la biblioteca asynq. Es el único camino asíncrono del sistema y maneja dos tipos de tarea, `media:scan` para el escaneo antimalware y `media:transcode` para la transcodificación. La API encola el trabajo y responde sin esperar, y el Go Worker lo consume por separado. El diagrama de componentes marca esa relación como *event driven* justamente por eso: la API no bloquea al usuario mientras se procesa el archivo, y el estado del recurso se consulta después. Toda la cola vive en una sola cola de asynq llamada `media`, y el worker le da prioridad sobre la cola por defecto.

El worker tiene dos dependencias de proceso que no son servicios de red. ClamAV escanea el archivo original antes de que el recurso pueda publicarse, y ffmpeg genera los derivados HLS a partir del original. Además de invocar esas dos herramientas, el worker escribe el estado del recurso en PostgreSQL para que la API lo pueda consultar. Los estados por los que pasa un recurso son `pending`, `processing`, `ready` y `failed`.

El almacenamiento de objetos es Cloud Storage y lo usan los dos lados, pero no con los mismos permisos. La API lo usa para emitir las URLs firmadas de subida y de descarga, y no toca los bytes. El worker sí mueve bytes, porque descarga el original para procesarlo y sube los segmentos generados. El diagrama separa las dos flechas hacia el mismo bucket precisamente por eso, porque son operaciones distintas con permisos distintos.

El servidor SMTP es Mailpit y existe solo para las pruebas. La API le entrega el correo de verificación de cuenta y el de recuperación de contraseña. No hay proveedor de correo real porque el enunciado no lo pide y el alcance de esta entrega es el despliegue, no las funcionalidades.

Las comunicaciones síncronas del sistema son HTTP entre el usuario y Caddy, y HTTP interno entre Caddy y la API. La única comunicación asíncrona es la cola, y es también la única que sobrevive a que el cliente se desconecte.

## Despliegue

Todo está en `us-central1`, en la zona `us-central1-a`, sobre una red virtual privada con una subred, `10.10.1.0/24`. Una sola zona y una sola subred bastan porque el enunciado pide un despliegue básico sin alta disponibilidad y sin réplicas entre zonas.

Hay dos máquinas de aplicación y una de pruebas. Las dos de aplicación son `e2-small`, con 2 vCPU, 2 GiB de memoria y 30 GiB de disco persistente balanceado. El sistema operativo es Debian GNU/Linux 12 en las tres.

En `mooc-web` corren tres contenedores: Caddy, la API y Mailpit. Esta máquina es la única con dirección pública, y por eso la regla de firewall `mooc-allow-web` deja entrar tráfico desde cualquier origen a los puertos 80 y 443. Todo lo demás queda cerrado desde fuera.

En `mooc-worker` corren otros tres contenedores: el worker de Go, Redis y ClamAV. Esta máquina no tiene dirección pública. Redis y ClamAV quedan accesibles solo por la red privada, que es exactamente lo que el enunciado pide para la cola de mensajería y para los servicios internos. La regla `mooc-allow-internal` permite el tráfico dentro de `10.10.1.0/24`, y la regla `mooc-allow-iap-ssh` deja administrar las dos máquinas por túnel de Identity-Aware Proxy sin abrir el puerto 22 a Internet. La primera máquina no necesita abrir el SSH porque el túnel lo maneja Google.

`mooc-loadgen` es la tercera máquina y no forma parte de la aplicación. Existe para ejecutar k6 desde dentro de la región, porque medir desde un portátil mide el enlace doméstico y no la plataforma. Se aprovisiona junto con el resto, cuesta como una máquina más mientras está encendida y se puede apagar cuando no se están tomando mediciones.

La base de datos administrada es Cloud SQL para PostgreSQL en el tipo `db-f1-micro`, con 20 GiB de almacenamiento, en una sola zona y sin réplica de lectura. Tiene la IPv4 deshabilitada, así que no acepta conexiones desde Internet y solo se llega a ella por la dirección privada. Los respaldos automáticos están activados y el almacenamiento puede crecer solo.

El almacenamiento de objetos son tres buckets de Cloud Storage, todos en la clase Standard y en la misma región. Uno guarda los originales que sube el profesor, otro los derivados HLS que genera el worker y otro los archivos públicos y las miniaturas. Separarlos permite dar permisos distintos a cada uno y hace más simple razonar sobre qué se puede borrar sin romper nada.

Los volúmenes persistentes son los discos de arranque de cada máquina más el volumen de Docker donde Redis guarda su archivo `appendonly`. El disco de arranque es lo que hace que un recurso de la máquina sobreviva a un reinicio, y es también lo que explica que una máquina detenida siga costando, porque el disco se sigue facturando por tamaño aprovisionado.

Cloud NAT da salida a Internet a las tres máquinas, que la necesitan para instalar dependencias durante el arranque. No hay balanceador de carga, ni escalado automático, ni réplicas entre máquinas, y todo eso queda fuera a propósito porque el enunciado delimita el alcance de esta entrega y lo deja para la arquitectura objetivo.

## Decisiones y adaptaciones

Los cambios frente a la entrega anterior son tres, y los tres vienen de requisitos del enunciado más que de una elección libre.

Redis se queda en contenedor y se muda al Worker Server. El enunciado pide que el sistema de mensajería se despliegue en un contenedor en esa máquina y que sea accesible por la red privada, así que la co-localización de Redis con el worker no fue una decisión de diseño sino una imposición. La consecuencia práctica es que comparten CPU y memoria, y que la concurrencia del worker queda limitada por lo que sobre después de que ClamAV y Redis tomen su parte. Esa restricción tiene efectos medibles que se detallan más abajo.

El almacenamiento local se reemplaza por Cloud Storage. Se conservaron la organización lógica de los objetos y los dos flujos que ya existían en la entrega anterior: la carga directa desde el cliente y el acceso por URL firmada. Lo que cambió es el destino. La carga directa es importante en términos de arquitectura porque el archivo nunca pasa por la API: el cliente sube los bytes directamente al bucket, y de ese modo la máquina que también sirve el proxy y la API no se ocupa del tráfico pesado.

PostgreSQL sale del contenedor y pasa a Cloud SQL. La conexión es por IP privada y las reglas de red están limitadas a las máquinas que la necesitan. La ganancia es que los respaldos, las actualizaciones menores y la durabilidad del disco dejan de ser responsabilidad del equipo. El costo es que aparece un recurso administrado que sigue cobrando aunque las máquinas estén apagadas, y que hay que acordarse de eliminarlo al terminar.

La configuración del worker también cambió, y no por una decisión de arquitectura sino por un fallo que se midió. Con concurrencia 10 el contenedor no cabía en los 2 GiB de la máquina. Diez procesos de ffmpeg procesando 1080p consumen alrededor de 1 017 MB, y sumando ClamAV, que oscila entre 320 y 850 MB según la base de firmas que tenga cargada, y el propio proceso de Go, la cuenta llegaba a unos 2 297 MB frente a 1 976 disponibles. El gestor de memoria del núcleo mataba los procesos de codificación a mitad, cada recurso quedaba en estado fallido y la cola se llenaba de trabajos muertos. Se bajó la concurrencia a 4, lo que deja 288 MB de margen para el pico de ClamAV, y se dejó declarada de forma explícita en la configuración en lugar de heredarla de un valor por defecto. El detalle de la medición está en `capacity-planning/pruebas_de_carga_entrega2.md`.

Frente a la arquitectura objetivo del proyecto faltan el balanceador de carga, el escalado automático, la réplica entre zonas y la distribución de contenido por CDN. El procesamiento tampoco escala, porque hoy hay una sola máquina de worker y la concurrencia es fija. Son piezas que la entrega deja explícitamente fuera de alcance, pero conviene tenerlas identificadas porque son las que marcan la distancia entre lo que hay y lo que el sistema debería llegar a ser.

## Operación y recuperación

El aprovisionamiento se hace con Terraform y el código vive en `scripts/IaC`. La única variable obligatoria es el identificador del proyecto, y el resto tiene valores por defecto razonables. El despliegue de la aplicación dentro de las máquinas no lo hace Terraform, sino los guiones de arranque, que clonan el repositorio, instalan las dependencias, construyen las imágenes y levantan los contenedores. La definición de los contenedores está en `scripts/IaC/deploy/docker-compose.cloud.yml`.

No hay ningún secreto en el repositorio. La contraseña de PostgreSQL, la de Redis, la clave de firma del JWT y las credenciales de interoperabilidad de Cloud Storage se guardan en Secret Manager, y los procesos las leen al arrancar. El archivo de variables de Terraform está excluido del control de versiones, y en las pruebas de capacidad las credenciales se inyectan en la sesión y nunca se pasan por metadatos de la máquina.

La verificación más simple de que el sistema está sano es el endpoint de salud. Responde en el Web Server y comprueba la conexión con PostgreSQL y con Redis, de modo que un fallo de cualquiera de las dos dependencias se ve de inmediato. Cuando la base de datos no responde devuelve un 500 con un mensaje que lo explica, y esa fue la señal que permitió diagnosticar el incidente de memoria que se describe en el informe de capacidad.

Para reiniciar basta con reiniciar los contenedores de la máquina correspondiente, y las políticas de reinicio los vuelven a levantar solos. Un reinicio del worker no pierde la cola, porque Redis guarda su estado en el volumen `appendonly`. Lo que sí pierde son las transcodificaciones que estuvieran a medio hacer, que vuelven a la cola como trabajos fallidos y reintentados. Eso ya ocurrió una vez y quedó contabilizado en el informe.

La migración desde el almacenamiento de la entrega anterior consistió en subir los objetos a los buckets conservando la organización lógica. Para esta entrega, además, el corpus de prueba se puede generar de nuevo con `task seed:all`, que deja los archivos y sus referencias en la base de datos. Eso hace que reconstruir el entorno no dependa de arrastrar binarios de una máquina a otra.

El respaldo tiene dos partes. La base de datos administrada tiene respaldos automáticos activados, y por el lado propio lo que hay que conservar es el corpus sintético y los guiones de siembra y de medición, que están en `capacity-planning/`. Con eso se puede reconstruir el entorno completo desde cero. La instancia de Cloud SQL debe eliminarse al terminar la entrega, después de conservar lo necesario, porque es el único recurso que sigue cobrando con las máquinas apagadas.

## Capacidad, costo y limitaciones

La configuración exacta es de dos máquinas `e2-small` con 2 vCPU, 2 GiB de memoria y 30 GiB de disco balanceado, una instancia de Cloud SQL `db-f1-micro` con 20 GiB, tres buckets de Cloud Storage en clase Standard y una pasarela de Cloud NAT.

### Costo por máquina

El estimador de precios desglosa cada máquina en dos renglones:

| elemento | estimación mensual |
|---|---|
| 2 CPU virtuales + 2 GB de memoria | USD 12,23 |
| Disco persistente balanceado de 30 GB | USD 3,00 |
| **total por máquina** | **USD 15,23** |

Son USD 0,02 por hora, con facturación por segundo y sin pagos por adelantado. Los renglones de registro y supervisión y de programación de instantáneas quedan como "el costo varía" en el estimador, porque dependen del volumen de métricas y de registros que se generen y no se pueden fijar de antemano. El Ops Agent de las dos máquinas de aplicación envía métricas y registros a Cloud Monitoring y Cloud Logging, y ese volumen depende de cuánto tiempo estén encendidas.

### Costos adicionales

Estos son los servicios que no son cómputo, enumerados punto por punto tal como los devuelve el estimador. La columna de cantidad lleva la unidad que usa el propio estimador, y las dos columnas de identificadores sirven para rastrear de dónde sale cada cifra.

| servicio | elemento | cantidad | región | service_id | sku | precio, USD |
|---|---|---|---|---|---|---|
| Secret Manager | Secret access operations | 100 | global | EE82-7A5E-871C | EBA7-264F-2D2C | 0,00 |
| Secret Manager | Secret version replica storage | 3 | global | EE82-7A5E-871C | 7756-ADEF-84F4 | 0,00 |
| PostgreSQL (Cloud SQL) | Cloud SQL for PostgreSQL: Zonal - Micro instance in Americas | 730 | us-central1 | 9662-B51E-5089 | C2D4-F7DF-B8D0 | 7,665 |
| PostgreSQL (Cloud SQL) | Cloud SQL for PostgreSQL: Zonal - Standard storage in Americas | 21 900 | us-central1 | 9662-B51E-5089 | C18F-7FE0-0717 | 5,10 |
| Cloud Storage | Standard Storage US Regional | 100 | us-central1 | 95FF-2EF5-5EA1 | E5F0-6A5D-7BAD | 1,90 |
| | **total adicional** | | | | | **14,665** |

Las dos líneas de Secret Manager quedan en cero porque el servicio tiene una franja gratuita que cubre de sobra los cinco secretos del despliegue y los pocos accesos que se hacen al arrancar los contenedores.

La instancia de Cloud SQL se cobra por 730 horas, que es el mes completo encendida. El almacenamiento se cobra por 21 900 GB-hora, que corresponde a 30 GB durante las 730 horas. Ese renglón conviene revisarlo, porque la instancia desplegada tiene 20 GiB de disco y no 30, así que la cifra real es menor.

Cloud Storage se cobra por 100 GB de almacenamiento regional Standard, que es una suposición de trabajo y no una medición. Los buckets del despliegue suman 4,55 GB reales, así que ese renglón está sobreestimado a propósito para dejar margen.

### Total

| escenario | cómputo | adicionales | total |
|---|---|---|---|
| dos máquinas de aplicación | 30,46 | 14,665 | **45,13** |
| con el generador encendido todo el mes | 45,69 | 14,665 | **60,36** |

El primer escenario es el que corresponde a la arquitectura entregada, porque el generador es una herramienta de prueba y el enunciado pide apagar los recursos cuando no se usan. El segundo es el techo, y sirve para comparar con una estimación que arrastre las tres máquinas.

### Lo que queda fuera de la estimación

Hay cuatro conceptos que sí se generan y que no aparecen en ninguna de las líneas anteriores.

El egreso a Internet no está cuantificado. Incluye la instalación de dependencias de las tres máquinas y, sobre todo, la salida de los segmentos HLS desde Cloud Storage hacia el cliente. En las pruebas salieron 1,93 GB en el patrón de descarga masiva y 68,6 MB en el de reproducción, pero esas cifras describen el enlace del portátil y no un consumo real con muchos estudiantes conectados.

Las operaciones de Cloud Storage de clase A y B se cobran aparte del almacenamiento y no tienen línea propia. En el consumo medido hubo 7 571 descargas de segmentos y manifiestos, más las escrituras de los derivados HLS durante las transcodificaciones.

La dirección externa del Web Server aparece en la estimación con precio cero. Google cobra las direcciones externas mientras están asociadas a una máquina encendida, así que ese renglón hay que verificarlo al hacer la estimación definitiva.

Los respaldos de Cloud SQL que excedan el tamaño de la instancia tampoco están contemplados. La instancia tiene los respaldos automáticos activados y el crecimiento de almacenamiento sin límite.

### Consumo observado

Sirve para contrastar el supuesto con lo que se midió durante las pruebas:

| concepto | observado |
|---|---|
| datos en Cloud Storage | 4,55 GB entre los tres buckets: 3,50 GB de originales, 1,06 GB de derivados HLS y 2,5 KB de archivos públicos |
| subidas directas aceptadas | 388 en el nivel más alto del escenario multimedia, más 4 del sembrado |
| segmentos descargados | 272 en el patrón de reproducción y 7 299 en el de descarga masiva |
| trabajos de transcodificación | 967 completados y 1 186 fallidos, de los que 509 corresponden al reinicio de las máquinas |
| peticiones a la API | 31 388 durante el escenario académico |
| transferencia desde Cloud Storage al cliente | 68,6 MB en el patrón de reproducción y 1,93 GB en el de descarga masiva |
| pico de transferencia de subida | 2,16 MB/s, limitado por el enlace del portátil |

### Puntos únicos de falla

Los puntos únicos de falla son varios y conviene enumerarlos con precisión, porque cada uno tiene una consecuencia distinta.

El Web Server es el único con dirección pública, así que si se cae no hay entrada a la plataforma. No hay segundo punto de entrada ni balanceador que pueda redirigir el tráfico.

El Worker Server concentra el worker, la cola y el antimalware en la misma máquina. Una falla de esa máquina detiene todo el procesamiento y además tumba Redis, y con Redis se cae la autenticación de toda la plataforma, porque las sesiones viven ahí y el Web Server las valida en cada petición. Esa dependencia cruzada ya se manifestó una vez: cuando el agotamiento de memoria mató a ClamAV, Redis dejó de responder comandos y las sesiones del Web Server dejaron de validarse, con lo que toda la plataforma quedó inutilizable aunque solo una dependencia de una máquina estaba caída.

La base de datos está en una sola zona y sin réplica, así que una falla de la zona afecta a todo el sistema.

### Hacia una aplicación elástica

Los cambios que acercarían esta configuración a una aplicación elástica son tres, y cada uno tiene una medición que lo respalda.

Separar el antimalware de la máquina que transcodifica. ClamAV retiene entre 320 y 850 MB de forma permanente para servir un escaneo que ocurre una sola vez por archivo, y ese consumo es la razón directa de que la concurrencia sea 4 y no 10. Moverlo a un servicio administrado, a un trabajo aislado o a una máquina aparte libera la restricción sin tocar la etapa de codificación.

Sacar Redis del Worker Server, o al menos aislarlo. Hoy comparte destino con los transcodificadores, y una caída de esa máquina arrastra la autenticación de toda la plataforma. Separarlo rompe esa dependencia cruzada.

Pasar a más de un worker. Hoy el procesamiento tiene un techo fijo de cuatro trabajos simultáneos, y se midió que la cola crece bastante más rápido de lo que se vacía, del orden de cuarenta veces. Con más de una máquina de worker la cola podría repartirse, y eso además elimina el punto único de falla del procesamiento.

Las mediciones que sostienen cada uno de esos tres cambios están en `capacity-planning/pruebas_de_carga_entrega2.md`.
