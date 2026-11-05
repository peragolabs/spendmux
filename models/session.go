package models

import (
	"time"

	"github.com/google/uuid"
)

// Session is a durable login cookie mapping (survives restarts / multi-replica).
type Session struct {
	Base
	UserID    uuid.UUID  `gorm:"type:uuid;index;not null"`
	OrgID     *uuid.UUID `gorm:"type:uuid"`
	ExpiresAt time.Time  `gorm:"index;not null"`
}
