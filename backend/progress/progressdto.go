package progress

// HeartbeatInput reporta reproducción/apertura. Percent/Completed están
// prohibidos: el avance lo calcula el servidor (se rechazan y auditan).
type HeartbeatInput struct {
	StableID    string   `json:"stable_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	PositionSec int      `json:"position_sec" example:"320"`
	DurationSec int      `json:"duration_sec" example:"600"`
	Event       string   `json:"event" example:"playing"`
	Percent     *float64 `json:"percent"`
	Completed   *bool    `json:"completed"`
}

// CourseProgressResponse resume el avance sobre recursos obligatorios.
type CourseProgressResponse struct {
	Total   int    `json:"total" example:"12"`
	Done    int    `json:"done" example:"9"`
	Percent int    `json:"percent" example:"75"`
	Status  string `json:"status" example:"in_progress"`
	Badge   string `json:"badge" example:"a1b2c3d4e5f6"`
}
