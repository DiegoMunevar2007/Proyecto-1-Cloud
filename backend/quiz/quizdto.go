package quiz

// CreateQuizRequest vincula reglas de intento a un recurso tipo quiz.
type CreateQuizRequest struct {
	ResourceID      uint   `json:"resource_id" example:"7"`
	AttemptsAllowed int    `json:"attempts_allowed" example:"3"`
	TimeLimitSec    *int   `json:"time_limit_sec" example:"600"`
	FeedbackPolicy  string `json:"feedback_policy" example:"on_submit"`
}

// AddQuestionsRequest agrega preguntas de selección única.
type AddQuestionsRequest struct {
	Questions []QuestionInput `json:"questions"`
}

// StudentQuestion es la vista sin clave correcta (lo único que ve el estudiante).
type StudentQuestion struct {
	Position int      `json:"position" example:"1"`
	Prompt   string   `json:"prompt" example:"¿Qué es una variable?"`
	Choices  []string `json:"choices" example:"Un valor con nombre,Un tipo,Otro,Otro más"`
}

// AttemptAnswers guarda respuestas parciales (posición → opción).
type AttemptAnswers struct {
	Answers map[string]int `json:"answers" example:"0:1"`
}

// AttemptResponse es la vista del intento para el estudiante.
// Score y Feedback solo aparecen tras el envío (y Feedback solo con on_submit).
type AttemptResponse struct {
	ID        uint              `json:"id" example:"1"`
	Status    string            `json:"status" example:"submitted"`
	Score     *int              `json:"score" example:"100"`
	Questions []StudentQuestion `json:"questions"`
	Answers   map[string]int    `json:"answers"`
	Feedback  []bool            `json:"feedback"`
}

// QuizEnvelope envuelve un quiz en la respuesta.
type QuizEnvelope struct {
	Quiz Quiz `json:"quiz"`
}

// QuestionsEnvelope envuelve preguntas creadas (vista de autoría, con clave).
type QuestionsEnvelope struct {
	Questions []Question `json:"questions"`
}

// QuizDetailResponse retorna quiz con preguntas (solo autoría).
type QuizDetailResponse struct {
	Quiz      Quiz       `json:"quiz"`
	Questions []Question `json:"questions"`
}
