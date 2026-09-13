package quiz

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/auth"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/courses"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/utils"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// _ conserva el import para las anotaciones swag.
var _ = utils.ErrorResponse{}

// Handler agrupa los endpoints de quizzes con sus dependencias.
type Handler struct {
	DB  *gorm.DB
	RDB *redis.Client
}

// SetupQuizRoutes registra las rutas de quizzes (autoría: professor/admin; intentos: autenticado).
func SetupQuizRoutes(router *gin.RouterGroup, db *gorm.DB, rdb *redis.Client) {
	h := &Handler{DB: db, RDB: rdb}
	requireAuthor := auth.RequireRole(rdb, auth.RoleProfessor, auth.RoleAdmin)
	requireAuth := auth.RequireAuth(rdb)
	router.POST("/quizzes", requireAuthor, h.Create)
	router.POST("/quizzes/:id/questions", requireAuthor, h.AddQuestions)
	router.GET("/quizzes/:id", requireAuthor, h.GetProfessor)
	router.DELETE("/quizzes/:id", requireAuthor, h.Delete)
	router.GET("/quizzes/:id/attempts", requireAuthor, h.ListAttempts)
	router.PUT("/quizzes/:id/questions/:qid", requireAuthor, h.UpdateQuestion)
	router.DELETE("/quizzes/:id/questions/:qid", requireAuthor, h.DeleteQuestion)
	router.POST("/quizzes/:id/attempts", requireAuth, h.Start)
	router.PUT("/attempts/:id", requireAuth, h.Save)
	router.POST("/attempts/:id/submit", requireAuth, h.Submit)
	router.GET("/attempts/:id", requireAuth, h.Get)
}

func writeErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, courses.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, ErrForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case errors.Is(err, courses.ErrImmutable), errors.Is(err, courses.ErrPublishedEdit):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, ErrAttemptsSpent), errors.Is(err, ErrAttemptExpired):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, ErrAttemptClosed):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, ErrInvalidPayload), errors.Is(err, ErrInvalidFeedback), errors.Is(err, ErrNotQuiz):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func studentOf(h *Handler, c *gin.Context) uint {
	uid, _ := auth.LookupUser(h.DB, c.GetString("username"))
	return uid
}

// studentView serializa el intento sin exponer jamás la clave correcta.
func studentView(db *gorm.DB, a *Attempt) AttemptResponse {
	var snap []SnapshotQuestion
	_ = json.Unmarshal([]byte(a.Snapshot), &snap)
	qs := make([]StudentQuestion, 0, len(snap))
	for i, q := range snap {
		qs = append(qs, StudentQuestion{Position: i + 1, Prompt: q.Prompt, Choices: q.Choices})
	}
	var answers map[string]int
	_ = json.Unmarshal([]byte(a.Answers), &answers)
	resp := AttemptResponse{ID: a.ID, Status: a.Status, Score: a.Score, Questions: qs, Answers: answers}
	if a.Status == AttemptSubmitted {
		var q Quiz
		feedback := true
		if err := db.First(&q, a.QuizID).Error; err != nil || q.FeedbackPolicy != FeedbackOnSubmit {
			feedback = false
		}
		if feedback {
			fb := make([]bool, 0, len(snap))
			for i, s := range snap {
				fb = append(fb, answers[strconv.Itoa(i)] == s.Correct)
			}
			resp.Feedback = fb
		}
	}
	return resp
}

// Create vincula reglas de intento a un recurso tipo quiz.
//
//	@Summary	Crear quiz
//	@Description	Define intentos, límite de tiempo y retroalimentación sobre un recurso quiz del borrador.
//	@Tags			Quizzes
//	@Produce		json
//	@Param			Authorization	header		string				true	"Bearer <token>"
//	@Param			request			body		CreateQuizRequest	true	"Reglas del quiz"
//	@Security		BearerAuth
//	@Success		201	{object}	QuizEnvelope	"Quiz creado"
//	@Failure		400	{object}	utils.ErrorResponse	"Datos inválidos"
//	@Failure		403	{object}	utils.ErrorResponse	"Sin permiso"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Failure		409	{object}	utils.ErrorResponse	"Versión inmutable"
//	@Router			/api/v1/quizzes [post]
func (h *Handler) Create(c *gin.Context) {
	var req CreateQuizRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.ResourceID == 0 {
		c.JSON(400, gin.H{"error": "resource_id requerido"})
		return
	}
	uid, role := auth.LookupUser(h.DB, c.GetString("username"))
	q, err := CreateQuiz(h.DB, uid, role, req.ResourceID, req.AttemptsAllowed, req.TimeLimitSec, req.FeedbackPolicy)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(201, gin.H{"quiz": q})
}

// AddQuestions agrega preguntas de selección única.
//
//	@Summary	Agregar preguntas
//	@Description	La clave correcta queda solo en servidor; el cliente jamás la recibe.
//	@Tags			Quizzes
//	@Produce		json
//	@Param			Authorization	header		string					true	"Bearer <token>"
//	@Param			id				path		int						true	"ID del quiz"
//	@Param			request			body		AddQuestionsRequest	true	"Preguntas"
//	@Security		BearerAuth
//	@Success		201	{object}	QuestionsEnvelope	"Preguntas creadas"
//	@Failure		400	{object}	utils.ErrorResponse	"Datos inválidos"
//	@Failure		403	{object}	utils.ErrorResponse	"Sin permiso"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Router			/api/v1/quizzes/{id}/questions [post]
func (h *Handler) AddQuestions(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	var req AddQuestionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "preguntas requeridas"})
		return
	}
	uid, role := auth.LookupUser(h.DB, c.GetString("username"))
	qs, err := AddQuestions(h.DB, uid, role, uint(id), req.Questions)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(201, gin.H{"questions": qs})
}

// GetProfessor retorna el quiz con preguntas y claves (solo autoría).
//
//	@Summary	Ver quiz (autoría)
//	@Description	Incluye correct_index; solo professor/admin propietarios.
//	@Tags			Quizzes
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token>"
//	@Param			id				path		int		true	"ID del quiz"
//	@Security		BearerAuth
//	@Success		200	{object}	QuizDetailResponse	"Quiz con preguntas"
//	@Failure		403	{object}	utils.ErrorResponse	"Sin permiso"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Router			/api/v1/quizzes/{id} [get]
func (h *Handler) GetProfessor(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	uid, role := auth.LookupUser(h.DB, c.GetString("username"))
	q, err := authorQuizRead(h.DB, uint(id), uid, role)
	if err != nil {
		writeErr(c, err)
		return
	}
	qs, _ := questionsOf(h.DB, q.ID)
	c.JSON(200, gin.H{"quiz": q, "questions": qs})
}

// Delete elimina quiz, preguntas e intentos (solo borrador editable).
//
//	@Summary	Eliminar quiz
//	@Description	Borra el quiz con sus preguntas e intentos. Solo en versión borrador de curso no publicado.
//	@Tags			Quizzes
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token>"
//	@Param			id				path		int		true	"ID del quiz"
//	@Security		BearerAuth
//	@Success		200	{object}	utils.MessageResponse	"Quiz eliminado"
//	@Failure		403	{object}	utils.ErrorResponse	"Sin permiso"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Failure		409	{object}	utils.ErrorResponse	"Versión inmutable o curso publicado"
//	@Router			/api/v1/quizzes/{id} [delete]
func (h *Handler) Delete(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	uid, role := auth.LookupUser(h.DB, c.GetString("username"))
	if err := DeleteQuiz(h.DB, uid, role, uint(id)); err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "quiz eliminado"})
}

// ListAttempts lista intentos con notas (solo autoría).
//
//	@Summary	Intentos del quiz
//	@Description	Visible para autor/admin: incluye respuestas y notas (la clave vive en las preguntas).
//	@Tags			Quizzes
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token>"
//	@Param			id				path		int		true	"ID del quiz"
//	@Param			page			query		int		false	"Página (base 1)"	default(1)
//	@Param			limit			query		int		false	"Resultados por página (máx 100)"	default(20)
//	@Security		BearerAuth
//	@Success		200	{object}	AttemptsResponse	"Intentos"
//	@Failure		403	{object}	utils.ErrorResponse	"Sin permiso"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Router			/api/v1/quizzes/{id}/attempts [get]
func (h *Handler) ListAttempts(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	uid, role := auth.LookupUser(h.DB, c.GetString("username"))
	items, total, err := ListAttempts(h.DB, uid, role, uint(id), page, limit)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(200, gin.H{"attempts": items, "total": total, "page": page, "limit": limit})
}

// UpdateQuestion edita una pregunta (solo borrador).
//
//	@Summary	Editar pregunta
//	@Description	Actualiza prompt, opciones y clave. Los intentos ya iniciados conservan su snapshot.
//	@Tags			Quizzes
//	@Produce		json
//	@Param			Authorization	header		string			true	"Bearer <token>"
//	@Param			id				path		int				true	"ID del quiz"
//	@Param			qid				path		int				true	"ID de la pregunta"
//	@Param			request			body		QuestionInput	true	"Pregunta"
//	@Security		BearerAuth
//	@Success		200	{object}	QuestionEnvelope	"Pregunta actualizada"
//	@Failure		400	{object}	utils.ErrorResponse	"Datos inválidos"
//	@Failure		403	{object}	utils.ErrorResponse	"Sin permiso"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Router			/api/v1/quizzes/{id}/questions/{qid} [put]
func (h *Handler) UpdateQuestion(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	qid, _ := strconv.ParseUint(c.Param("qid"), 10, 32)
	var req QuestionInput
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "pregunta requerida"})
		return
	}
	uid, role := auth.LookupUser(h.DB, c.GetString("username"))
	q, err := UpdateQuestion(h.DB, uid, role, uint(id), uint(qid), req)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(200, gin.H{"question": q})
}

// DeleteQuestion elimina una pregunta (solo borrador).
//
//	@Summary	Eliminar pregunta
//	@Description	Los intentos existentes conservan su snapshot y siguen calificando.
//	@Tags			Quizzes
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token>"
//	@Param			id				path		int		true	"ID del quiz"
//	@Param			qid				path		int		true	"ID de la pregunta"
//	@Security		BearerAuth
//	@Success		200	{object}	utils.MessageResponse	"Pregunta eliminada"
//	@Failure		403	{object}	utils.ErrorResponse	"Sin permiso"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Router			/api/v1/quizzes/{id}/questions/{qid} [delete]
func (h *Handler) DeleteQuestion(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	qid, _ := strconv.ParseUint(c.Param("qid"), 10, 32)
	uid, role := auth.LookupUser(h.DB, c.GetString("username"))
	if err := DeleteQuestion(h.DB, uid, role, uint(id), uint(qid)); err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "pregunta eliminada"})
}

// Start inicia o retoma el intento en borrador. Idempotente por Idempotency-Key.//
//	@Summary	Iniciar intento
//	@Description	Congela un snapshot de preguntas; reintento con la misma clave retorna el mismo intento.
//	@Tags			Quizzes
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token>"
//	@Param			Idempotency-Key	header		string	false	"Clave de idempotencia"
//	@Param			id				path		int		true	"ID del quiz"
//	@Security		BearerAuth
//	@Success		201	{object}	AttemptResponse	"Intento"
//	@Failure		403	{object}	utils.ErrorResponse	"Sin derecho o intentos agotados"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Router			/api/v1/quizzes/{id}/attempts [post]
func (h *Handler) Start(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	a, err := StartAttempt(h.DB, studentOf(h, c), uint(id), c.GetHeader("Idempotency-Key"))
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(201, studentView(h.DB, a))
}

// Save guarda respuestas parciales del borrador.
//
//	@Summary	Guardado parcial
//	@Description	Solo sobre intentos en borrador vigente; valida rangos en servidor.
//	@Tags			Quizzes
//	@Produce		json
//	@Param			Authorization	header		string			true	"Bearer <token>"
//	@Param			id				path		int				true	"ID del intento"
//	@Param			request			body		AttemptAnswers	true	"Respuestas (posición → opción)"
//	@Security		BearerAuth
//	@Success		200	{object}	AttemptResponse	"Intento actualizado"
//	@Failure		400	{object}	utils.ErrorResponse	"Respuesta fuera de rango"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Failure		409	{object}	utils.ErrorResponse	"Intento cerrado o expirado"
//	@Router			/api/v1/attempts/{id} [put]
func (h *Handler) Save(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	var req AttemptAnswers
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "answers requerido"})
		return
	}
	a, err := SavePartial(h.DB, studentOf(h, c), uint(id), req.Answers)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(200, studentView(h.DB, a))
}

// Submit califica en servidor. Idempotente: reenvío retorna el mismo resultado.
//
//	@Summary	Enviar intento
//	@Description	Calcula la nota en servidor desde el snapshot; nunca confía en el cliente.
//	@Tags			Quizzes
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token>"
//	@Param			id				path		int		true	"ID del intento"
//	@Security		BearerAuth
//	@Success		200	{object}	AttemptResponse	"Calificado"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Failure		409	{object}	utils.ErrorResponse	"Intento cerrado o expirado"
//	@Router			/api/v1/attempts/{id}/submit [post]
func (h *Handler) Submit(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	a, err := Submit(h.DB, studentOf(h, c), uint(id))
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(200, studentView(h.DB, a))
}

// Get retorna el intento del estudiante (sin claves correctas).
//
//	@Summary	Ver intento
//	@Description	Score y feedback solo tras el envío, según la política del quiz.
//	@Tags			Quizzes
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token>"
//	@Param			id				path		int		true	"ID del intento"
//	@Security		BearerAuth
//	@Success		200	{object}	AttemptResponse	"Intento"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Router			/api/v1/attempts/{id} [get]
func (h *Handler) Get(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	var a Attempt
	if err := h.DB.Where("id = ? AND student_id = ?", uint(id), studentOf(h, c)).First(&a).Error; err != nil {
		c.JSON(404, gin.H{"error": "no encontrado"})
		return
	}
	c.JSON(200, studentView(h.DB, &a))
}
