package models

import (
	"time"

	"github.com/google/uuid"
)

// LoopFingerprintBucket counts identical proxy requests on one virtual key
// so a retry loop can 429 before the provider is called again.
type LoopFingerprintBucket struct {
	PlatformAPIKeyID uuid.UUID      `gorm:"type:uuid;primaryKey"`
	Fingerprint      string         `gorm:"size:64;primaryKey"`
	WindowStartedAt  time.Time      `gorm:"not null;index"`
	RequestCount     int64          `gorm:"not null"`
	PlatformAPIKey   PlatformAPIKey `gorm:"foreignKey:PlatformAPIKeyID;constraint:OnDelete:CASCADE"`
}
