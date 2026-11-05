package models

import (
	"time"

	"github.com/google/uuid"
)

// User is a signed-in account, plus FiberLab workspace extras.
//
// Column mapping from legacy FiberLab:
//
//	oauth_provider  → Provider
//	oauth_id        → ProviderID
//	full_name       → Name
//	is_admin        → Role ("admin" | "user")
//	encrypted_email → Email (encrypted at rest via the email serializer)
type User struct {
	Base
	Username   string `gorm:"unique"`
	Image      string
	Provider   string
	ProviderID string `gorm:"column:provider_id;unique"`
	Name       string
	Email      string `gorm:"serializer:email"` // encrypted at rest; lookups use EmailHash
	AvatarURL  string `json:"avatar_url"`
	Role       string `gorm:"default:user" json:"role"`
	Credits    int    `gorm:"default:0" json:"credits"`

	KeepRequestHistory bool `gorm:"default:true" json:"keep_request_history"`

	// FiberLab extras
	EmailHash             string     `gorm:"index" json:"-"`
	DefaultOrgID          *uuid.UUID `gorm:"type:uuid;index" json:"default_org_id,omitempty"`
	OnboardingCompletedAt *time.Time `json:"onboarding_completed_at,omitempty"`
}

func (u User) IsAdmin() bool {
	return u.Role == "admin"
}
