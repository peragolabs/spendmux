package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Base replaces gorm.Model with UUID support.
// Soft-delete is non-negotiable — token revocation depends on it.
type Base struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;"`
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

// BeforeCreate generates a UUID if one doesn't exist.
func (base *Base) BeforeCreate(tx *gorm.DB) error {
	if base.ID == uuid.Nil {
		base.ID = uuid.New()
	}
	return nil
}
