package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestInstrumentRequestUsesRouteTemplate verifica que las métricas se etiquetan
// con la plantilla de la ruta y no con la URL pedida. Usar el URL haría que cada
// :id generara una serie nueva y el scrape de /metrics crecería sin límite en un
// despliegue real.
func TestInstrumentRequestUsesRouteTemplate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	before := testutil.ToFloat64(
		requestsTotal.WithLabelValues("/api/v1/courses/:id", http.MethodGet, "200"))

	router := gin.New()
	router.Use(instrumentRequest())
	router.GET("/api/v1/courses/:id", func(c *gin.Context) { c.Status(http.StatusOK) })

	for _, id := range []string{"1", "2", "3", "42", "999999"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/courses/"+id, nil)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("id %s: se esperaba 200 y se obtuvo %d", id, w.Code)
		}
	}

	after := testutil.ToFloat64(
		requestsTotal.WithLabelValues("/api/v1/courses/:id", http.MethodGet, "200"))
	if delta := after - before; delta != 5 {
		t.Errorf("se esperaban 5 peticiones en una sola serie y se registraron %.0f", delta)
	}

	// Ninguna serie debe haberse creado con el id en la etiqueta de ruta.
	for _, id := range []string{"1", "2", "3", "42", "999999"} {
		if got := testutil.ToFloat64(requestsTotal.WithLabelValues(
			"/api/v1/courses/"+id, http.MethodGet, "200")); got != 0 {
			t.Errorf("se creó una serie con la URL concreta %q", id)
		}
	}
}

// TestInstrumentRequestSkipsMetricsEndpoint comprueba que el propio scrape no se
// mide a sí mismo: hacerlo contaría las peticiones de telemetría como carga de la
// API que se está midiendo.
func TestInstrumentRequestSkipsMetricsEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	before := testutil.ToFloat64(requestsTotal.WithLabelValues("/metrics", http.MethodGet, "200"))

	router := gin.New()
	router.Use(instrumentRequest())
	router.GET("/metrics", gin.WrapH(metricsHandler()))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	after := testutil.ToFloat64(requestsTotal.WithLabelValues("/metrics", http.MethodGet, "200"))
	if delta := after - before; delta != 0 {
		t.Errorf("el scrape se instrumentalizó: %.0f peticiones contadas", delta)
	}
}

// TestMetricsHandlerServesScrape verifica el contrato del endpoint: un scrape
// Prometheus con la etiqueta component=api, las métricas HTTP y las de la cola.
func TestMetricsHandlerServesScrape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(instrumentRequest())
	router.GET("/metrics", gin.WrapH(metricsHandler()))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("se esperaba 200 y se obtuvo %d", w.Code)
	}
	// promhttp negocia el formato según el Accept del scraper: lo que se fija
	// aquí es que se sirve texto 0.0.4 y no protobuf, que el Ops Agent no lee.
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
		t.Errorf("Content-Type inesperado: %q", ct)
	}
	body := w.Body.String()
	for _, want := range []string{
		`go_goroutines{component="api"}`,
		`process_resident_memory_bytes{component="api"}`,
		`mooc_http_requests_inflight{component="api"}`,
		// El estado de la cola se scrapea desde la API, que es el único proceso
		// con acceso a Redis, y por eso debe aparecer en este mismo scrape.
		`mooc_queue_pending{component="api"}`,
		`mooc_queue_age_seconds{component="api"}`,
		`mooc_queue_scrape_ok{component="api"}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("falta %q en /metrics", want)
		}
	}
	// La etiqueta no debe ser job=: el receptor de Prometheus del Ops Agent
	// convierte job_name en una etiqueta job y colisionaría con esta.
	if strings.Contains(body, `job="api"`) {
		t.Error("el scrape usa la etiqueta job, que colisiona con la del receptor")
	}
}
