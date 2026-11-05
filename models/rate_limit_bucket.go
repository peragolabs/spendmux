package models

import (
	"time"

	"github.com/google/uuid"
)

// PlatformKeyRateLimitBucket is the shared fixed-window request counter for a
// platform key. PostgreSQL keeps enforcement consistent across app replicas.
type PlatformKeyRateLimitBucket struct {
	PlatformAPIKeyID uuid.UUID      `gorm:"type:uuid;primaryKey"`
	WindowStartedAt  time.Time      `gorm:"not null;index"`
	RequestCount     int64          `gorm:"not null"`
	TokenCount       int64          `gorm:"not null;default:0"`
	PlatformAPIKey   PlatformAPIKey `gorm:"foreignKey:PlatformAPIKeyID;constraint:OnDelete:CASCADE"`
}
