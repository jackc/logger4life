package domain

import "time"

// LogEntry is the business representation of a recorded event. It contains
// no persistence-specific values.
type LogEntry struct {
	ID         string         `json:"id"`
	LogID      string         `json:"log_id"`
	UserID     string         `json:"user_id"`
	Username   string         `json:"username"`
	Fields     map[string]any `json:"fields"`
	OccurredAt time.Time      `json:"occurred_at"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

// LogPlacementChange describes a user's requested folder and sibling order.
type LogPlacementChange struct {
	FolderID *string `json:"folder_id"`
	Position int     `json:"position"`
}

type HomePinChange struct {
	Pinned bool `json:"pinned"`
}
type HomeOrderChange struct {
	HomePosition int `json:"home_position"`
}
