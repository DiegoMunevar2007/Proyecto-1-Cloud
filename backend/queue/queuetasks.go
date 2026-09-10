package queue

import "encoding/json"

// Tipos de tareas asynq. La API publica, el worker consume.
const (
	TypeMediaTranscode = "media:transcode"
	TypeMediaProbe     = "media:probe"
)

// TranscodePayload es el payload para transcodificar un recurso a HLS.
type TranscodePayload struct {
	ResourceID uint   `json:"resource_id"`
	ObjectKey  string `json:"object_key"`
	MimeType   string `json:"mime_type"`
	// IdempotencyKey evita salidas repetidas ante entregas duplicadas.
	IdempotencyKey string `json:"idempotency_key"`
}

// Encode serializa el payload a JSON.
func Encode(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

// DecodeTranscode deserializa un payload de transcodificación.
func DecodeTranscode(data []byte) (TranscodePayload, error) {
	var p TranscodePayload
	err := json.Unmarshal(data, &p)
	return p, err
}
