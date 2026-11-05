package models

import (
	"time"

	"github.com/google/uuid"
)

// MemoryThread is one opt-in conversation memory (X-Spendmux-Memory) for a
// platform token. Messages are stored encrypted under a key derived from the
// raw bearer token, which the server keeps only as a hash: the rows are
// unreadable at rest, even with the database and every server secret.
type MemoryThread struct {
	Base
	UserID           uuid.UUID `gorm:"type:uuid;index" json:"user_id"`
	PlatformAPIKeyID uuid.UUID `gorm:"type:uuid;uniqueIndex:idx_memory_key_thread" json:"platform_api_key_id"`
	// ThreadHash is sha256(token id, thread id): thread ids often embed end-user ids.
	ThreadHash string    `gorm:"size:64;uniqueIndex:idx_memory_key_thread" json:"-"`
	Ciphertext string    `gorm:"type:text" json:"-"`
	Messages   int       `json:"messages"`
	ExpiresAt  time.Time `gorm:"index" json:"expires_at"`
}
