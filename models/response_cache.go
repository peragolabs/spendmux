package models

import (
	"time"

	"github.com/google/uuid"
)

// ResponseCache is one cached provider reply (opt-in, X-Spendmux-Cache or a
// token's cache TTL). ID and the encryption key are both derived from the
// server secret and the canonical request, so a row can be found and read
// only by someone sending that same request. Rows are hard-deleted on expiry.
type ResponseCache struct {
	ID               string    `gorm:"primaryKey;size:64"`
	UserID           uuid.UUID `gorm:"type:uuid;index"`
	Endpoint         string    `gorm:"size:50"`
	Provider         string    `gorm:"size:50"`
	Model            string    `gorm:"size:150"`
	Ciphertext       []byte    `gorm:"type:bytea"`
	PromptTokens     int
	CompletionTokens int
	CostUSDMicros    int64 // what the original call cost; a hit saves this much
	Hits             int
	CreatedAt        time.Time
	ExpiresAt        time.Time `gorm:"index"`
}
