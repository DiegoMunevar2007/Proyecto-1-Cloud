package queue

import "encoding/json"

// Tipos de tareas asynq. La API publica, el worker consume.
const (
	TypeMediaTranscode = "media:transcode"
	TypeMediaScan      = "media:scan"
)

// TranscodePayload es el payload para transcodificar un recurso a HLS.
type TranscodePayload struct {
	ResourceID uint   `json:"resource_id"`
	ObjectKey  string `json:"object_key"`
	MimeType   string `json:"mime_type"`
	// IdempotencyKey evita salidas repetidas ante entregas duplicadas.
	IdempotencyKey string `json:"idempotency_key"`
}

// DecodeTranscode deserializa un payload de transcodificación.
func DecodeTranscode(data []byte) (TranscodePayload, error) {
	var p TranscodePayload
	err := json.Unmarshal(data, &p)
	return p, err
}

// ScanPayload es el payload para el escaneo antimalware de un objeto.
// TranscodeKey se reenvía al transcode si el archivo está limpio y es multimedia.
type ScanPayload struct {
	ResourceID   uint   `json:"resource_id"`
	ObjectKey    string `json:"object_key"`
	TranscodeKey string `json:"transcode_key"`
}

// DecodeScan deserializa un payload de escaneo antimalware.
func DecodeScan(data []byte) (ScanPayload, error) {
	var p ScanPayload
	err := json.Unmarshal(data, &p)
	return p, err
}
