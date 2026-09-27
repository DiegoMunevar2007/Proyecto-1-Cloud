# Plataforma MOOC

Aplicación de cursos masivos abiertos en línea, desarrollada como monolito modular en Go con workers independientes, cola de mensajes y almacenamiento de objetos. Esta entrega corresponde al despliegue básico sobre Google Cloud: dos máquinas virtuales para la aplicación, una instancia administrada de PostgreSQL y los objetos en Cloud Storage.

## Aplicación desplegada

La API responde en `https://35.255.181.251`. El certificado es autofirmado, así que el navegador o el cliente HTTP pedirá aceptarlo la primera vez. El endpoint `https://35.255.181.251/health` sirve para comprobar que la plataforma está arriba: además de responder, verifica la conexión con PostgreSQL y con Redis.

Las cuentas de prueba y las credenciales se entregan por el canal privado del curso y no se publican aquí.

## Documentación

El documento de arquitectura está en [`docs/entrega2/arquitectura.md`](docs/entrega2/arquitectura.md). Explica los componentes y sus responsabilidades, el modelo de despliegue, las decisiones y adaptaciones frente a la entrega anterior, el procedimiento de operación y recuperación, y el costo con sus limitaciones.

Los diagramas de componentes y de despliegue están en [`docs/entrega2/diagramas/`](docs/entrega2/diagramas/).

El informe de capacidad de los dos escenarios está en [`capacity-planning/pruebas_de_carga_entrega2.md`](capacity-planning/pruebas_de_carga_entrega2.md), con las gráficas en [`capacity-planning/graficas/`](capacity-planning/graficas/).

## Despliegue

La infraestructura se aprovisiona con Terraform desde `scripts/IaC`. La única variable obligatoria es el identificador del proyecto:

```bash
cd scripts/IaC
terraform init
terraform apply -var="project_id=TU_PROYECTO"
```

Los guiones de arranque de las máquinas clonan este repositorio, instalan las dependencias y levantan los contenedores. La definición de los contenedores está en `scripts/IaC/deploy/docker-compose.cloud.yml`.

Los secretos se guardan en Secret Manager y se inyectan en ejecución. No hay credenciales en el repositorio ni en las imágenes.

## Pruebas

El corpus sintético y las corridas de carga se manejan con las tareas del `Taskfile` de `capacity-planning`. Hacen falta las herramientas `k6` y `go-task`:

```bash
cd capacity-planning
export ENV=gcp
export ADMIN_USER=admin1 ADMIN_PASS='...' SEED_PASSWORD='...'
task seed:all      # siembra el corpus
task esc1:all      # escenario 1
task esc2:all      # escenario 2
```

Las corridas deben ejecutarse desde la máquina `mooc-loadgen`, que se aprovisiona con el resto, para no medir el enlace del portátil. Cada corrida deja un resumen y un archivo de muestras en `capacity-planning/results/`, y los resultados de esta entrega están versionados en el repositorio.

## Estructura

- `backend/`: la API en Go, organizada por módulos de negocio.
- `backend/worker/`: el worker que consume la cola, escanea y transcodifica.
- `capacity-planning/`: guiones de k6, datos de prueba, resultados, gráficas y el informe de capacidad.
- `docs/entrega2/`: documentación de arquitectura, diagramas y evidencia del despliegue.
- `scripts/IaC/`: infraestructura como código y guiones de arranque de las máquinas.
