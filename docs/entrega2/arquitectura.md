# Arquitectura desplegada (Entrega 2)

Plataforma MOOC sobre Google Cloud. Este documento describe lo que quedó desplegado y en qué se diferencia de la entrega anterior. Los dos diagramas están en la raíz del repositorio: `Diagrama Componentes Proyecto 2.png` y `Diagrama Despliegue Proyecto 2.png`.

## Componentes

El diagrama de componentes muestra las piezas de la aplicación y sus interfaces. El usuario entra por Caddy, que es el único punto público. Caddy hace de proxy inverso y termina el TLS, y detrás está la API, que es un monolito modular en Go.

Los módulos de la API son `auth`, `admin`, `courses`, `enroll`, `quiz`, `progress`, `badges`, `uploads`, `storage`, `mail`, `queue` y `telemetry`. Cada uno expone su grupo de rutas y comparte la misma conexión a la base de datos, el mismo cliente de Redis y el mismo cliente de almacenamiento. No son servicios independientes: viven en un solo proceso y se comunican por llamadas directas, no por red.

La base de datos es Cloud SQL para PostgreSQL. Guarda cuentas, estructura académica, recursos y metadatos, inscripciones, intentos, calificaciones, progreso y los trabajos de multimedia. La API la consulta por SQL sobre la IP privada.

La cola de mensajería es Redis, con la biblioteca asynq. Redis es el único camino asíncrono del sistema y tiene dos tipos de tarea, `media:scan` y `media:transcode`. La API encola y el Go Worker consume. Esa relación es la que el diagrama marca como *event driven*: la API no espera al worker, responde cuando el trabajo queda encolado y el estado del recurso se consulta después.

El worker tiene dos dependencias de proceso. ClamAV escanea el archivo original antes de que pueda publicarse, y ffmpeg genera los derivados HLS. También escribe el estado del recurso en PostgreSQL, para que la API lo pueda consultar.

El almacenamiento de objetos es Cloud Storage y lo usan los dos lados. La API lo usa para emitir URLs firmadas de subida y de descarga; el worker lo usa para leer el original y para escribir los segmentos generados. El diagrama separa las dos flechas hacia el mismo bucket justamente por eso: no es la misma operación ni el mismo permiso.

El servidor SMTP es Mailpit y solo existe para las pruebas. La API le entrega el correo de verificación de cuenta y de recuperación. En esta entrega no hay proveedor de correo real porque el enunciado no lo pide y el alcance es el despliegue.

Las comunicaciones síncronas son HTTP entre el usuario y Caddy, y entre Caddy y la API. El worker habla con PostgreSQL y con Cloud Storage. La única comunicación asíncrona es la cola.

## Despliegue

El diagrama de despliegue muestra dónde vive cada pieza. Todo está en `us-central1`, zona `us-central1-a`, sobre una VPC con una subred, `10.10.1.0/24`.

Hay dos máquinas de aplicación y una de pruebas. Las dos de aplicación son `e2-small`, con 2 vCPU, 2 GiB de RAM y 30 GiB de disco `pd-balanced`.

En `mooc-web` corren Caddy, la API y Mailpit como contenedores. Esta máquina es la única con IP pública, y por eso el firewall `mooc-allow-web` deja entrar `0.0.0.0/0` a los puertos 80 y 443.

En `mooc-worker` corren el worker, Redis y ClamAV. No tiene IP pública. Redis y ClamAV quedan accesibles solo por la red privada, que es lo que pide el enunciado para la cola y para los servicios internos. La regla `mooc-allow-internal` abre el tráfico dentro de `10.10.1.0/24`, y `mooc-allow-iap-ssh` permite la administración por túnel de Identity-Aware Proxy sin abrir el 22 a Internet.

`mooc-loadgen` es la tercera máquina y no forma parte de la aplicación. Existe para ejecutar k6 desde dentro de la región y no medir el enlace del portátil. Se aprovisiona con las pruebas y se puede apagar cuando no se mide.

La base de datos administrada es Cloud SQL para PostgreSQL en `db-f1-micro` con 20 GiB de almacenamiento, en una sola zona y sin réplica de lectura. Tiene la IPv4 deshabilitada y solo se llega a ella por la IP privada.

El almacenamiento de objetos son tres buckets de Cloud Storage, todos en la clase Standard y en la misma región: uno para los originales, uno para los derivados HLS y uno para los archivos públicos y las miniaturas.

Los volúmenes persistentes son los discos de arranque de cada máquina más el volumen de Docker donde Redis guarda su `appendonly`. El disco de arranque es lo que hace que un recurso de la máquina sobreviva a un reinicio, y es también lo que explica que una máquina detenida siga costando.

Cloud NAT da salida a Internet a las tres máquinas, que la necesitan para instalar dependencias. No hay balanceador de carga, ni escalado automático, ni réplicas entre máquinas, porque el enunciado delimita el alcance y deja esas piezas para la arquitectura objetivo.

## Decisiones y adaptaciones

Los cambios frente a la entrega anterior son tres, y los tres vienen del enunciado.

Redis se queda en contenedor y se muda al Worker Server. El enunciado pide que el sistema de mensajería se despliegue en un contenedor en esa máquina y que sea accesible por la red privada, así que la co-localización con el worker no fue una elección de diseño sino un requisito. La consecuencia práctica es que comparten CPU y memoria, y que la concurrencia del worker está limitada por lo que sobre después de ClamAV y Redis.

El almacenamiento local se reemplaza por Cloud Storage. Se conservó la organización lógica de los objetos y los dos flujos que ya existían: carga directa desde el cliente y acceso por URL firmada. Lo que cambió es el destino. La carga directa significa que el archivo nunca pasa por la API, lo que descarga a la máquina que también sirve el API y el proxy.

PostgreSQL sale del contenedor y pasa a Cloud SQL. La conexión es por IP privada y con reglas limitadas a las máquinas que la necesitan. La ganancia es que los respaldos y las actualizaciones dejan de ser responsabilidad del equipo, y el costo es que aparece un componente administrado que hay que recordar eliminar al terminar.

La configuración del worker también cambió, y no por una decisión de arquitectura sino por un fallo medido. Con concurrencia 10 el contenedor no cabía en los 2 GiB de la máquina: diez ffmpeg de 1080p más clamd y el proceso de Go sumaban unos 2 297 MB frente a 1 976 disponibles, y el gestor de memoria mataba los procesos de codificación a mitad. Se bajó a 4 y se dejó declarado de forma explícita en lugar de heredarlo de un valor por defecto. El detalle y la medición están en el informe de capacidad.

Frente a la arquitectura objetivo del proyecto, faltan el balanceador de carga, el escalado automático, la réplica entre zonas y la CDN. El procesamiento tampoco escala: hoy hay una sola máquina de worker y una concurrencia fija. Son las piezas que la entrega deja explícitamente fuera de alcance.

## Operación y recuperación

El aprovisionamiento es con Terraform y vive en `scripts/IaC`. La única variable obligatoria es el identificador del proyecto. El despliegue de la aplicación dentro de las máquinas lo hacen los guiones de arranque, que clonan el repositorio, construyen las imágenes y levantan los contenedores. Los propios contenedores se definen en `scripts/IaC/deploy/docker-compose.cloud.yml`.

No hay ningún secreto en el repositorio. Las contraseñas de PostgreSQL, de Redis, la clave del JWT y las credenciales de interoperabilidad de Cloud Storage se guardan en Secret Manager y los procesos las leen al arrancar. El archivo de variables de Terraform está fuera del control de versiones, y del lado de las pruebas las credenciales se inyectan en la sesión y nunca se pasan por metadatos de la máquina.

La verificación más simple es el endpoint de salud, que responde en el Web Server y además comprueba la conexión con PostgreSQL y con Redis. Si la base de datos no responde, devuelve 500 con un mensaje que lo dice, y eso fue lo que permitió diagnosticar el incidente de memoria descrito en el informe de capacidad.

Para reiniciar basta con reiniciar los contenedores en la máquina correspondiente, y las políticas de reinicio los vuelven a levantar. Un reinicio del worker no pierde la cola, porque Redis guarda su estado en el volumen `appendonly`; sí pierde las transcodificaciones que estuvieran a medio hacer, que vuelven a la cola como fallidas y reintentadas.

La migración desde el almacenamiento anterior fue subir los objetos a los buckets conservando la organización lógica, y en esta entrega el corpus de prueba se genera de nuevo con `task seed:all`, que deja los archivos y sus referencias en la base de datos. Eso hace que la reconstrucción del entorno no dependa de arrastrar binarios.

El respaldo tiene dos partes. La base de datos administrada tiene respaldos automáticos activados, y por el lado propio lo que hay que conservar es el corpus sintético y los guiones, que están en `capacity-planning/`. Con eso se puede reconstruir el entorno desde cero. La instancia de Cloud SQL debe eliminarse al terminar la entrega, porque es el único recurso que sigue cobrando con las máquinas apagadas.

## Capacidad, costo y limitaciones

La configuración exacta es de dos máquinas `e2-small` con 2 vCPU, 2 GiB de RAM y 30 GiB de disco balanceado, una instancia de Cloud SQL `db-f1-micro` con 20 GiB, tres buckets de Cloud Storage y una pasarela de Cloud NAT. El detalle de los tipos, las cantidades y los precios está en `docs/entrega2/costos.md`, con los valores recogidos del despliegue real.

El consumo observado durante las pruebas fue de 4,55 GB en Cloud Storage, 31 388 peticiones a la API en el escenario académico, 388 subidas directas aceptadas en el nivel más alto del escenario multimedia y 7 571 descargas de segmentos. La estimación mensual con las máquinas encendidas de forma permanente queda alrededor de 61 dólares, y ese número es un techo: las máquinas estuvieron apagadas la mayor parte del tiempo y el enunciado pide justamente apagarlas cuando no se usan.

Los puntos únicos de falla son varios y conviene enumerarlos. El Web Server es el único con IP pública, así que si se cae no hay entrada a la plataforma. El Worker Server concentra el worker, la cola y el antimalware, de modo que una falla de esa máquina detiene todo el procesamiento y además tumba Redis, y con Redis se cae la autenticación de toda la plataforma porque las sesiones viven ahí. Esa dependencia cruzada ya se manifestó una vez, cuando el agotamiento de memoria de ClamAV dejó a Redis sin responder comandos y las sesiones del Web Server dejaron de validarse. La base de datos está en una sola zona y sin réplica, así que una falla de la zona afecta a todo el sistema.

Los cambios que acercarían esto a una aplicación elástica son tres. Separar el antimalware de la máquina que transcodifica, porque ClamAV retiene entre 320 y 850 MB de forma permanente para servir un escaneo que ocurre una sola vez por archivo, y ese consumo es la razón de que la concurrencia sea 4 y no 10. Sacar Redis del Worker Server, o al menos aislarlo, porque hoy comparte destino con los transcodificadores y su caída arrastra la autenticación. Y pasar la cola a un modelo con más de un worker, porque hoy el procesamiento tiene un techo fijo de cuatro trabajos simultáneos y la cola crece más rápido de lo que se vacía. Las mediciones que sostienen cada uno de esos cambios están en `capacity-planning/pruebas_de_carga_entrega2.md`.
