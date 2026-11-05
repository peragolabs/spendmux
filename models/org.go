package models

import (
	"time"

	"github.com/google/uuid"
)

type Org struct {
	Base
	Name       string    `gorm:"not null"`
	Slug       *string   `gorm:"uniqueIndex"`
	IsPersonal bool      `gorm:"not null;default:false"`
	CreatedBy  uuid.UUID `gorm:"type:uuid;not null;index"`
}

type OrgMember struct {
	Base
	OrgID  uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:ux_org_member"`
	UserID uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:ux_org_member;index"`
	Role   string    `gorm:"size:32;not null;default:member"` // owner|admin|member
}

type OrgInvite struct {
	Base
	OrgID      uuid.UUID `gorm:"type:uuid;not null;index"`
	EmailHash  string    `gorm:"not null;index"`
	Email      string    `gorm:"not null;serializer:email"`
	Role       string    `gorm:"size:32;not null;default:member"`
	TokenHash  string    `gorm:"not null;index"`
	InvitedBy  uuid.UUID `gorm:"type:uuid;not null"`
	ExpiresAt  time.Time `gorm:"not null"`
	AcceptedAt *time.Time
}
