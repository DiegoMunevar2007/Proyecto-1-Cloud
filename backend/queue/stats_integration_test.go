package queue

import (
	"os"
	"testing"
	"time"

	"github.com/hibiken/asynq"
)

// Estas pruebas hablan con un Redis real, no con un doble: el error que motivó
// la prueba viene de cómo asynq propaga el fallo de Redis cuando la cola no
// existe, y un doble no reproduce esa semántica.
//
// Se omiten si no hay Redis. Para correrlas:
//
//	docker compose up -d redis
//	REDIS_TEST_ADDR=localhost:6379 go test ./queue/ -run TestInspect -v
func addrDePrueba(t *testing.T) string {
	t.Helper()
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("REDIS_TEST_ADDR no está definido; se omite la prueba de integración")
	}
	return addr
}

// colaDePrueba usa un nombre propio para no tocar la cola media de la aplicación.
func colaDePrueba(t *testing.T) (string, func()) {
	t.Helper()
	addr := addrDePrueba(t)
	t.Setenv("REDIS_ADDR", addr)
	nombre := "test-queue-" + time.Now().Format("20060102150405.000000")
	limpiar := func() {
		inspector := asynq.NewInspector(RedisOpt())
		defer inspector.Close()
		_ = inspector.DeleteQueue(nombre, true)
	}
	return nombre, limpiar
}

// TestInspectQueueInexistente cubre el caso de un despliegue recién arrancado,
// o de una cola ya drenada y borrada: no es un error, es el estado normal. Antes
// de corregirlo, este caso devolvía 500 con el error crudo de Redis.
func TestInspectQueueInexistente(t *testing.T) {
	nombre, limpiar := colaDePrueba(t)
	defer limpiar()

	stats, err := InspectQueue(nombre)
	if err != nil {
		t.Fatalf("una cola inexistente no debe ser un error, se obtuvo: %v", err)
	}
	if stats.Exists {
		t.Error("Exists debe ser false en una cola que no está en Redis")
	}
	if stats.Pending != 0 || stats.Size != 0 || stats.AgeOldestPendingSec != 0 {
		t.Errorf("una cola inexistente debe reportar cero en sus contadores, se obtuvo %+v", stats)
	}
	if stats.Queue != nombre {
		t.Errorf("Queue = %q, se esperaba %q", stats.Queue, nombre)
	}
}

// TestInspectQueueConTrabajos comprueba el camino normal y que la antigüedad del
// pendiente se calcula.
func TestInspectQueueConTrabajos(t *testing.T) {
	nombre, limpiar := colaDePrueba(t)
	defer limpiar()

	client := asynq.NewClient(RedisOpt())
	defer client.Close()
	// asynq dedupea por task id, así que los dos trabajos necesitan ids distintos.
	for _, id := range []string{"t1", "t2"} {
		info, err := client.Enqueue(asynq.NewTask(TypeMediaScan, []byte(`{"resource_id":1}`)),
			asynq.Queue(nombre), asynq.TaskID(id), asynq.MaxRetry(3))
		if err != nil {
			t.Fatalf("encolar %s: %v", id, err)
		}
		_ = info
	}

	stats, err := InspectQueue(nombre)
	if err != nil {
		t.Fatalf("InspectQueue: %v", err)
	}
	if !stats.Exists {
		t.Fatal("la cola existe y Exists debe ser true")
	}
	if stats.Pending != 2 {
		t.Errorf("Pending = %d, se esperaba 2", stats.Pending)
	}
	if stats.Size < 2 {
		t.Errorf("Size = %d, se esperaba al menos 2", stats.Size)
	}
	if stats.AgeOldestPendingSec < 0 {
		t.Errorf("AgeOldestPendingSec no puede ser negativo: %v", stats.AgeOldestPendingSec)
	}
	if stats.ObservedAt.IsZero() {
		t.Error("ObservedAt debe informarse para saber de cuándo es la lectura")
	}
}
