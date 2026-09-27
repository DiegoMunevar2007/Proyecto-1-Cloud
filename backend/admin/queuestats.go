package admin

import (
	"net/http"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/queue"
	"github.com/gin-gonic/gin"
)

// QueueStatsResponse agrupa las colas inspeccionadas en una sola respuesta.
type QueueStatsResponse struct {
	Queues []queue.QueueStats `json:"queues"`
}

// QueueStats expone el estado de la cola de trabajos asíncronos.
//
// Lo consume el generador de pruebas de carga para correlacionar la latencia
// observada con la profundidad y la antigüedad de la cola, ya que el worker no
// expone ningún endpoint y la red de pruebas no alcanza Redis.
//
//	@Summary		Estado de la cola de trabajos
//	@Description	Instantánea de la cola asynq leída desde Redis: trabajos pendientes, en ejecución, programados, en reintento y archivados (DLQ), antigüedad del pendiente más viejo y totales procesados y fallidos. Si la cola aún no existe responde con exists=false y todos los contadores en cero.
//	@Tags			Administración
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token de admin>"
//	@Security		BearerAuth
//	@Success		200	{object}	QueueStatsResponse	"Estado de la cola"
//	@Failure		401	{object}	utils.ErrorResponse	"No autenticado"
//	@Failure		403	{object}	utils.ErrorResponse	"Se requiere rol admin"
//	@Failure		500	{object}	utils.ErrorResponse	"No se pudo leer la cola"
//	@Router			/api/v1/admin/queue [get]
func (h *Handler) QueueStats(c *gin.Context) {
	stats, err := queue.InspectMediaQueue()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, QueueStatsResponse{Queues: []queue.QueueStats{*stats}})
}
