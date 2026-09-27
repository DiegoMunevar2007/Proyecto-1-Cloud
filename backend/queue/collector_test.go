package queue

import (
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// newTestCollector registra el collector en un registro nuevo y lo devuelve junto
// con la función de lectura de valores.
func newTestCollector(t *testing.T, p StatsProvider) (*Collector, func() map[string]float64) {
	t.Helper()
	c := NewCollector(p)
	registry := prometheus.NewRegistry()
	if err := c.Register(prometheus.WrapRegistererWith(prometheus.Labels{"component": "api"}, registry)); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return c, func() map[string]float64 {
		t.Helper()
		families, err := registry.Gather()
		if err != nil {
			t.Fatalf("Gather: %v", err)
		}
		out := map[string]float64{}
		for _, mf := range families {
			for _, m := range mf.GetMetric() {
				switch {
				case m.Gauge != nil:
					out[mf.GetName()] = m.Gauge.GetValue()
				case m.Counter != nil:
					out[mf.GetName()] = m.Counter.GetValue()
				}
			}
		}
		return out
	}
}

func estado() *QueueStats {
	return &QueueStats{
		Queue:               MediaQueue,
		Exists:              true,
		Pending:             12,
		Active:              10,
		Retry:               2,
		Archived:            1,
		AgeOldestPendingSec: 43.5,
		MemoryUsage:         20481,
		ProcessedTotal:      318,
		FailedTotal:         4,
	}
}

func TestCollectorPublicaElEstado(t *testing.T) {
	_, read := newTestCollector(t, func() (*QueueStats, error) { return estado(), nil })
	got := read()

	for name, want := range map[string]float64{
		"mooc_queue_exists":          1,
		"mooc_queue_pending":         12,
		"mooc_queue_active":          10,
		"mooc_queue_retry":           2,
		"mooc_queue_archived":        1,
		"mooc_queue_age_seconds":     43.5,
		"mooc_queue_memory_bytes":    20481,
		"mooc_queue_scrape_ok":       1,
		"mooc_queue_processed_total": 318,
		"mooc_queue_failed_total":    4,
	} {
		if got[name] != want {
			t.Errorf("%s = %v, se esperaba %v", name, got[name], want)
		}
	}
}

// TestCollectorConvierteTotalesEnIncrementos es la razón de ser del collector:
// asynq entrega acumulados, no incrementos, y Prometheus necesita que el
// contador solo suba para poder calcular rate().
func TestCollectorConvierteTotalesEnIncrementos(t *testing.T) {
	st := estado()
	_, read := newTestCollector(t, func() (*QueueStats, error) { return st, nil })

	read() // primera lectura: el acumulado base se toma como punto de partida
	if got := read()["mooc_queue_processed_total"]; got != 318 {
		t.Fatalf("tras la primera lectura processed_total = %v, se esperaba 318", got)
	}

	st.ProcessedTotal = 318 // sin cambio
	if got := read()["mooc_queue_processed_total"]; got != 318 {
		t.Errorf("sin trabajo nuevo el contador cambió a %v", got)
	}

	st.ProcessedTotal = 350 // 32 trabajos más
	if got := read()["mooc_queue_processed_total"]; got != 350 {
		t.Errorf("processed_total = %v, se esperaba 350", got)
	}

	st.FailedTotal = 9
	if got := read()["mooc_queue_failed_total"]; got != 9 {
		t.Errorf("failed_total = %v, se esperaba 9", got)
	}
}

// TestCollectorIgnoraTotalesQueRetroceden cubre el caso en que la cola se borra
// y se recrea: el acumulado de asynq vuelve a cero y un Counter de Prometheus no
// puede retroceder.
func TestCollectorIgnoraTotalesQueRetroceden(t *testing.T) {
	st := estado()
	_, read := newTestCollector(t, func() (*QueueStats, error) { return st, nil })
	read()

	st.ProcessedTotal = 5 // cola recreada
	if got := read()["mooc_queue_processed_total"]; got != 318 {
		t.Errorf("processed_total = %v: un total menor no debe mover el contador", got)
	}
}

// TestCollectorNoPublicaCerosSiRedisFalla es la propiedad de seguridad del
// análisis de capacidad: si la lectura falla y se publicaran ceros, el informe
// concluiría que la cola drenó, que es justo la conclusión equivocada.
func TestCollectorNoPublicaCerosSiRedisFalla(t *testing.T) {
	st := estado()
	var failure error
	_, read := newTestCollector(t, func() (*QueueStats, error) { return st, failure })
	read() // establishes known values

	failure = errors.New("redis no responde")
	got := read()

	if got["mooc_queue_scrape_ok"] != 0 {
		t.Error("scrape_ok debe quedar en 0 cuando la lectura falla")
	}
	if got["mooc_queue_scrape_errors_total"] != 1 {
		t.Errorf("scrape_errors_total = %v, se esperaba 1", got["mooc_queue_scrape_errors_total"])
	}
	if got["mooc_queue_pending"] != 12 {
		t.Errorf("pending = %v: debe conservar el último valor conocido, no volver a 0", got["mooc_queue_pending"])
	}

	failure = nil
	got = read()
	if got["mooc_queue_scrape_ok"] != 1 {
		t.Error("scrape_ok debe volver a 1 al recuperarse la lectura")
	}
}

func TestColaInexistenteEsEstadoValido(t *testing.T) {
	// Una cola que nunca recibió trabajos no es un error: exists=0 y todo en cero.
	vacia := &QueueStats{Queue: MediaQueue}
	_, read := newTestCollector(t, func() (*QueueStats, error) { return vacia, nil })
	got := read()

	if got["mooc_queue_exists"] != 0 {
		t.Error("exists debe ser 0 si la cola no está en Redis")
	}
	if got["mooc_queue_scrape_ok"] != 1 {
		t.Error("una cola inexistente no es un fallo de lectura")
	}
	if got["mooc_queue_pending"] != 0 || got["mooc_queue_age_seconds"] != 0 {
		t.Error("una cola inexistente debe reportar sus contadores en cero")
	}
}
