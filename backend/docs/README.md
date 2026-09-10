# Especificación OpenAPI de la API

`openapi.json` / `openapi.yaml` son la especificación **OpenAPI 3.1** de la API,
con descripciones en español. Se **generan** desde anotaciones `swag` en el código
(`swaggo/swag` v2, flag `--v3.1`); no se editan a mano.

## Regenerar

```bash
# Instalar el generador (versión pineada, fuera del go.mod)
go install github.com/swaggo/swag/v2/cmd/swag@v2.0.0

# Generar desde backend/ y renombrar (swag emite swagger.* por defecto)
cd backend
swag init -g main.go -o docs --ot json,yaml --v3.1 --packageName docs
mv docs/swagger.json docs/openapi.json
mv docs/swagger.yaml docs/openapi.yaml
```

El CI (`openapi-check`) regenera y falla si el spec commiteado deriva del código.

## Consumir

- **En vivo**: `GET /openapi.json` (embebido en el binario con `go:embed`).
- **Bruno**: Open Collection → Import → archivo `openapi.json`.
- **Postman**: Import → `openapi.json` (OpenAPI 3.1). Autenticación: variable
  `token` con el JWT de `/auth/login`, header `Authorization: Bearer {{token}}`.

## Convenciones para nuevos endpoints

1. Handlers como **métodos nombrados** (`func (h *Handler) X(c *gin.Context)`);
   swag no ve closures anónimos.
2. Requests/responses como **structs nombrados** (`*dto.go`) con `example:`.
3. Anotaciones en español: `@Summary`, `@Description`, `@Tags`, `@Security BearerAuth`
   donde aplique, y `@Param Idempotency-Key header` en operaciones idempotentes.
4. Errores con `utils.ErrorResponse`; mensajes simples con `utils.MessageResponse`.

## Notas conocidas

- `GET /admin/users*` retorna la representación pública (sin hash de contraseña).
- La salida `--v3.1` conserva claves raíz 2.0 (`host`, `basePath`, `schemes`);
  no afectan validadores ni la importación en Bruno/Postman.
