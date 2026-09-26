# Colección Bruno — MOOC API

Evidencia reproducible de los flujos §5–§6 contra el sistema desplegado.

## Uso

1. `docker compose up` (API, worker, postgres, redis, s3, mailpit, clamav).
2. `cd bruno && npm install` (una vez; instala `@usebruno/cli`).
3. `npm test` (= `bru run --env Local`) o abrir `bruno/` en la app Bruno.
4. Ejecutar carpetas en orden `01 → 10`. Cada request encadena IDs vía variables
   de entorno (`courseId`, `attemptId`, `badgeCode`, …); no hay pasos manuales:
   los códigos de verificación se extraen de Mailpit automáticamente.
5. La carpeta `10-teardown` borra lo creado (cursos en cascada, quiz,
   recurso, inscripciones, usuario desechable) y verifica la ausencia.

## Leer el resultado del CLI

`bru run` devuelve exit 0 y `✓ PASS` **aunque fallen assertions**.
El veredicto real es el conteo de errores:

```bash
./node_modules/.bin/bru run --env Local 2>&1 | grep -c "Test script execution error"
# 0 = verde
```

## Notas

- **Re-ejecución:** los usuarios persisten entre runs (`Register` da 409 la
  segunda vez y los códigos expiran a los 10 min). Para un run completo usa
  DB fresca: `docker compose down -v && docker compose up`. El teardown
  deja cursos e inscripciones limpios, pero no usuarios.
- **04-upload** usa TUS (`POST /api/v1/uploads` → `PATCH` → `HEAD`
  con `Upload-Offset` verificado). `PublishB` lleva `await bru.sleep(15000)`
  de colchón para el scan+clamd tras completar la subida.
- Sintaxis de archivos en CLI: `file: @file(fixtures/x) @contentType(mime)`
  (la forma sin `@file()` envía cuerpo vacío sin avisar).
- Peculiaridades del CLI (v2.15, ya aplicadas en la colección):
- **06-quiz**: las respuestas correctas están fijadas en `AddQuestions`
  (`{0:1, 1:0}`) para que `Summary` llegue a `approved` y emita insignia.
- **08-badges**: revoca la insignia; para repetir el ciclo completo usa
  usuarios/estudiante nuevos (cambia `studentUser`/`studentEmail` en el entorno).
- `Staging.bru` es plantilla: completa hosts y contraseñas fuera del repo.
  Nunca commitear tokens reales en `environments/`.
- `fixtures/sample.txt` (53 B) y `fixtures/sample.mp4` (1 s, generado con
  el ffmpeg del worker) son los binarios de prueba TUS.
- `PublishB` lleva `await bru.sleep(15000)` en pre-request: el worker
  necesita segundos para el scan+clamav tras completar la subida.
- Peculiaridades del CLI (v2.15, ya aplicadas en la colección):
  en scripts usa `bru.getEnvVar()` (no `bru.getVar()`) para variables de
  entorno, evita bloques `params:query` (se pierden; pon el query inline
  en la URL) y cuenta `Tests 0/0` aunque los `tests {}` sí se ejecutan.
- **Cobertura negativa**: `03-crossowner` (profe2 vs curso de profe1),
  `LoginBlocked`/`LastAdminProtected` (02), `*Denied/*Forbidden/*NotFound`
  repartidos (401/403/404/409 según diseño). Nada rompe los tokens del flujo.
- `PatchWrongOffset` deja un multipart S3 abandonado a propósito: su `.info`
  expira por lifecycle (`tus-meta/`, 7 días), pero el multipart incompleto
  requiere el barrido programado pendiente. Ver notas TUS.
