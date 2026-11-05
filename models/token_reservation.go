package models

import (
	"time"

	"github.com/google/uuid"
)

// TokenReservation is an in-flight allowance held before an upstream request.
// Expiry converts an unverified settlement into conservatively estimated usage.
type TokenReservation struct {
	Base
	PlatformAPIKeyID uuid.UUID `gorm:"type:uuid;index"`
	Tokens           int64     `gorm:"not null"`
	USDMicros        int64     `gorm:"not null;default:0"`
	SessionID        string    `gorm:"size:200;index"`
	TenantID         string    `gorm:"size:200;index"`
	ExpiresAt        time.Time `gorm:"index;not null"`
	// ChargeOnExpiry marks reservations written by code that tags usage rows
	// with reservation_id. Only those can be settled into estimated usage
	// safely; rows from older releases (false after AutoMigrate) are dropped
	// on expiry as before, since their usage rows carry no reservation_id and
	// would be charged twice.
	ChargeOnExpiry bool           `gorm:"not null;default:false"`
	PlatformAPIKey PlatformAPIKey `gorm:"foreignKey:PlatformAPIKeyID;constraint:OnDelete:CASCADE"`
}
