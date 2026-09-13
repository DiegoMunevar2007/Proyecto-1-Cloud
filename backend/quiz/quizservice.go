package quiz

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/courses"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/enroll"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrNotQuiz         = errors.New("el recurso no es tipo quiz")
	ErrAttemptsSpent   = errors.New("intentos agotados")
	ErrAttemptClosed   = errors.New("el intento ya fue enviado")
	ErrAttemptExpired  = errors.New("el intento expiró")
	ErrForbidden       = errors.New("sin derecho de acceso")
	ErrInvalidPayload  = errors.New("datos inválidos")
	ErrInvalidFeedback = errors.New("feedback inválido (on_submit, never)")
)

// QuestionInput crea preguntas (selección única).
type QuestionInput struct {
	Prompt       string   `json:"prompt"`
	Choices      []string `json:"choices"`
	CorrectIndex int      `json:"correct_index"`
}

// authorQuizRead verifica propiedad o admin sin exigir borrador (lectura).
func authorQuizRead(db *gorm.DB, quizID uint, userID uint, role string) (*Quiz, error) {
	var q Quiz
	if err := db.First(&q, quizID).Error; err != nil {
		return nil, courses.ErrNotFound
	}
	_, c, _, err := courses.LocateResource(db, q.ResourceID)
	if err != nil {
		return nil, err
	}
	if !courses.IsOwnerOrAdmin(c, userID, role) {
		return nil, ErrForbidden
	}
	return &q, nil
}

// authorQuiz carga quiz + recurso + curso verificando propiedad o admin y borrador editable.
func authorQuiz(db *gorm.DB, quizID uint, userID uint, role string) (*Quiz, *courses.Resource, *courses.Course, error) {
	var q Quiz
	if err := db.First(&q, quizID).Error; err != nil {
		return nil, nil, nil, courses.ErrNotFound
	}
	r, c, v, err := courses.LocateResource(db, q.ResourceID)
	if err != nil {
		return nil, nil, nil, err
	}
	if !courses.IsOwnerOrAdmin(c, userID, role) {
		return nil, nil, nil, ErrForbidden
	}
	if v.IsImmutable || v.Status != courses.VersionStatusDraft {
		return nil, nil, nil, courses.ErrImmutable
	}
	if c.Status == courses.CourseStatusPublished {
		return nil, nil, nil, courses.ErrPublishedEdit
	}
	return &q, r, c, nil
}

// CreateQuiz vincula reglas de intento a un recurso tipo quiz del borrador.
func CreateQuiz(db *gorm.DB, userID uint, role string, resourceID uint, attempts int, limitSec *int, policy string) (*Quiz, error) {
	r, c, v, err := courses.LocateResource(db, resourceID)
	if err != nil {
		return nil, err
	}
	if !courses.IsOwnerOrAdmin(c, userID, role) {
		return nil, ErrForbidden
	}
	if v.IsImmutable || v.Status != courses.VersionStatusDraft {
		return nil, courses.ErrImmutable
	}
	if c.Status == courses.CourseStatusPublished {
		return nil, courses.ErrPublishedEdit
	}
	if courses.NormalizeResourceType(r.Type) != courses.ResourceTypeQuiz {
		return nil, ErrNotQuiz
	}
	if policy == "" {
		policy = FeedbackOnSubmit
	}
	if policy != FeedbackOnSubmit && policy != FeedbackNever {
		return nil, ErrInvalidFeedback
	}
	if attempts <= 0 {
		attempts = 3
	}
	var existing Quiz
	if err := db.Where("resource_id = ?", resourceID).First(&existing).Error; err == nil {
		return &existing, nil
	}
	q := &Quiz{ResourceID: resourceID, AttemptsAllowed: attempts, TimeLimitSec: limitSec, FeedbackPolicy: policy}
	return q, db.Create(q).Error
}

// AddQuestions agrega preguntas al final (posiciones autoincrementales).
func AddQuestions(db *gorm.DB, userID uint, role string, quizID uint, inputs []QuestionInput) ([]Question, error) {
	q, _, _, err := authorQuiz(db, quizID, userID, role)
	if err != nil {
		return nil, err
	}
	if len(inputs) == 0 {
		return nil, fmt.Errorf("%w: preguntas requeridas", ErrInvalidPayload)
	}
	var pos struct{ Max int }
	db.Model(&Question{}).Where("quiz_id = ?", q.ID).Select("COALESCE(MAX(position),0) AS max").Scan(&pos)
	out := make([]Question, 0, len(inputs))
	for i, in := range inputs {
		if len(in.Choices) < 2 {
			return nil, fmt.Errorf("%w: cada pregunta requiere ≥2 opciones", ErrInvalidPayload)
		}
		if in.CorrectIndex < 0 || in.CorrectIndex >= len(in.Choices) {
			return nil, fmt.Errorf("%w: correct_index fuera de rango", ErrInvalidPayload)
		}
		choices, _ := json.Marshal(in.Choices)
		qq := Question{QuizID: q.ID, Position: pos.Max + i + 1, Prompt: in.Prompt, Choices: string(choices), CorrectIndex: in.CorrectIndex}
		if err := db.Create(&qq).Error; err != nil {
			return nil, err
		}
		out = append(out, qq)
	}
	return out, nil
}

// QuizByResource resuelve el quiz de un recurso (para progreso/aprobación).
func QuizByResource(db *gorm.DB, resourceID uint) (*Quiz, error) {
	var q Quiz
	if err := db.Where("resource_id = ?", resourceID).First(&q).Error; err != nil {
		return nil, courses.ErrNotFound
	}
	return &q, nil
}

func questionsOf(db *gorm.DB, quizID uint) ([]Question, error) {
	var qs []Question
	return qs, db.Where("quiz_id = ?", quizID).Order("position asc").Find(&qs).Error
}

func snapshotOf(qs []Question) (string, []SnapshotQuestion, error) {
	snap := make([]SnapshotQuestion, 0, len(qs))
	for _, q := range qs {
		var choices []string
		if err := json.Unmarshal([]byte(q.Choices), &choices); err != nil {
			return "", nil, err
		}
		snap = append(snap, SnapshotQuestion{Prompt: q.Prompt, Choices: choices, Correct: q.CorrectIndex})
	}
	b, err := json.Marshal(snap)
	return string(b), snap, err
}

// StartAttempt inicia (o retoma) el intento en borrador. Idempotente por Idempotency-Key.
func StartAttempt(db *gorm.DB, studentID, quizID uint, idemKey string) (*Attempt, error) {
	var q Quiz
	if err := db.First(&q, quizID).Error; err != nil {
		return nil, courses.ErrNotFound
	}
	r, c, _, err := courses.LocateResource(db, q.ResourceID)
	if err != nil {
		return nil, err
	}
	_ = r
	if c.Status != courses.CourseStatusPublished {
		return nil, ErrForbidden
	}
	if !enroll.IsEnrolled(db, studentID, c.ID) {
		return nil, ErrForbidden
	}
	if idemKey != "" {
		var dup Attempt
		if err := db.Where("idempotency_key = ?", idemKey).First(&dup).Error; err == nil {
			return &dup, nil
		}
	}
	var draft Attempt
	if err := db.Where("quiz_id = ? AND student_id = ? AND status = ?", quizID, studentID, AttemptDraft).Order("id desc").First(&draft).Error; err == nil {
		return &draft, nil
	}
	var submitted int64
	db.Model(&Attempt{}).Where("quiz_id = ? AND student_id = ? AND status = ?", quizID, studentID, AttemptSubmitted).Count(&submitted)
	if submitted >= int64(q.AttemptsAllowed) {
		return nil, ErrAttemptsSpent
	}
	qs, err := questionsOf(db, q.ID)
	if err != nil {
		return nil, err
	}
	if len(qs) == 0 {
		return nil, fmt.Errorf("%w: el quiz no tiene preguntas", ErrInvalidPayload)
	}
	snap, _, err := snapshotOf(qs)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	a := &Attempt{QuizID: q.ID, StudentID: studentID, Snapshot: snap, Answers: "{}", Status: AttemptDraft, StartedAt: now}
	if q.TimeLimitSec != nil {
		exp := now.Add(time.Duration(*q.TimeLimitSec) * time.Second)
		a.ExpiresAt = &exp
	}
	if idemKey == "" {
		idemKey = uuid.NewString()
	}
	a.IdempotencyKey = idemKey
	return a, db.Create(a).Error
}

// SavePartial guarda respuestas parciales (solo borrador vigente).
func SavePartial(db *gorm.DB, studentID, attemptID uint, answers map[string]int) (*Attempt, error) {
	var a Attempt
	if err := db.Where("id = ? AND student_id = ?", attemptID, studentID).First(&a).Error; err != nil {
		return nil, courses.ErrNotFound
	}
	if a.Status != AttemptDraft {
		return nil, ErrAttemptClosed
	}
	if expired(&a) {
		a.Status = AttemptExpired
		_ = db.Save(&a).Error
		return nil, ErrAttemptExpired
	}
	var snap []SnapshotQuestion
	if err := json.Unmarshal([]byte(a.Snapshot), &snap); err != nil {
		return nil, err
	}
	for k, v := range answers {
		i, err := strconv.Atoi(k)
		if err != nil || i < 0 || i >= len(snap) || v < 0 || v >= len(snap[i].Choices) {
			return nil, fmt.Errorf("%w: respuesta fuera de rango", ErrInvalidPayload)
		}
	}
	b, _ := json.Marshal(answers)
	a.Answers = string(b)
	return &a, db.Save(&a).Error
}

// Submit califica en servidor. Idempotente: reenvío retorna el mismo resultado.
func Submit(db *gorm.DB, studentID, attemptID uint) (*Attempt, error) {
	var a Attempt
	if err := db.Where("id = ? AND student_id = ?", attemptID, studentID).First(&a).Error; err != nil {
		return nil, courses.ErrNotFound
	}
	if a.Status == AttemptSubmitted {
		return &a, nil
	}
	if a.Status != AttemptDraft {
		return nil, ErrAttemptClosed
	}
	if expired(&a) {
		a.Status = AttemptExpired
		_ = db.Save(&a).Error
		return nil, ErrAttemptExpired
	}
	var snap []SnapshotQuestion
	if err := json.Unmarshal([]byte(a.Snapshot), &snap); err != nil {
		return nil, err
	}
	var answers map[string]int
	_ = json.Unmarshal([]byte(a.Answers), &answers)
	score := Grade(snap, answers)
	now := time.Now()
	a.Score = &score
	a.Status = AttemptSubmitted
	a.SubmittedAt = &now
	return &a, db.Save(&a).Error
}

// Grade calcula la nota 0-100 en servidor a partir del snapshot (con clave).
func Grade(snap []SnapshotQuestion, answers map[string]int) int {
	if len(snap) == 0 {
		return 0
	}
	hit := 0
	for i, q := range snap {
		if answers[strconv.Itoa(i)] == q.Correct {
			hit++
		}
	}
	return hit * 100 / len(snap)
}

// Passed indica si el estudiante aprobó el quiz (algún intento enviado ≥ minPct).
func Passed(db *gorm.DB, studentID, quizID uint, minPct int) bool {
	var n int64
	db.Model(&Attempt{}).Where("quiz_id = ? AND student_id = ? AND status = ? AND score >= ?", quizID, studentID, AttemptSubmitted, minPct).Count(&n)
	return n > 0
}

// ListAttempts lista intentos del quiz (autoría), página base 1.
func ListAttempts(db *gorm.DB, userID uint, role string, quizID uint, page, limit int) ([]Attempt, int64, error) {
	if _, err := authorQuizRead(db, quizID, userID, role); err != nil {
		return nil, 0, err
	}
	if page <= 0 {
		page = 1
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var total int64
	if err := db.Model(&Attempt{}).Where("quiz_id = ?", quizID).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []Attempt
	err := db.Where("quiz_id = ?", quizID).Order("created_at desc").Offset((page - 1) * limit).Limit(limit).Find(&out).Error
	return out, total, err
}

// UpdateQuestion edita prompt/opciones/clave (solo borrador).
func UpdateQuestion(db *gorm.DB, userID uint, role string, quizID, questionID uint, in QuestionInput) (*Question, error) {
	if _, _, _, err := authorQuiz(db, quizID, userID, role); err != nil {
		return nil, err
	}
	var q Question
	if err := db.Where("id = ? AND quiz_id = ?", questionID, quizID).First(&q).Error; err != nil {
		return nil, courses.ErrNotFound
	}
	if len(in.Choices) < 2 {
		return nil, fmt.Errorf("%w: cada pregunta requiere ≥2 opciones", ErrInvalidPayload)
	}
	if in.CorrectIndex < 0 || in.CorrectIndex >= len(in.Choices) {
		return nil, fmt.Errorf("%w: correct_index fuera de rango", ErrInvalidPayload)
	}
	choices, _ := json.Marshal(in.Choices)
	q.Prompt = in.Prompt
	q.Choices = string(choices)
	q.CorrectIndex = in.CorrectIndex
	return &q, db.Save(&q).Error
}

// DeleteQuestion elimina una pregunta (solo borrador).
func DeleteQuestion(db *gorm.DB, userID uint, role string, quizID, questionID uint) error {
	if _, _, _, err := authorQuiz(db, quizID, userID, role); err != nil {
		return err
	}
	if err := db.Where("id = ? AND quiz_id = ?", questionID, quizID).Delete(&Question{}).Error; err != nil {
		return err
	}
	return nil
}

// DeleteQuiz elimina quiz, preguntas e intentos (solo borrador editable).
func DeleteQuiz(db *gorm.DB, userID uint, role string, quizID uint) error {
	q, _, _, err := authorQuiz(db, quizID, userID, role)
	if err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("quiz_id = ?", q.ID).Delete(&Attempt{}).Error; err != nil {
			return err
		}
		if err := tx.Where("quiz_id = ?", q.ID).Delete(&Question{}).Error; err != nil {
			return err
		}
		return tx.Delete(&Quiz{}, q.ID).Error
	})
}

func expired(a *Attempt) bool {
	return a.ExpiresAt != nil && time.Now().After(*a.ExpiresAt)
}
