package models

import "github.com/google/uuid"

// FallbackStep is one hop in a user-defined availability ladder.
type FallbackStep struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// FallbackChain is the ordered ladder used when a /v1 call sets "fallback": true.
// One chain per user for now (dev).
type FallbackChain struct {
	Base
	UserID    uuid.UUID `gorm:"type:uuid;uniqueIndex" json:"user_id"`
	StepsJSON string    `gorm:"type:text" json:"-"` // JSON array of FallbackStep
}
