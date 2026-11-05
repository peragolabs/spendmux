package models

import "github.com/google/uuid"

// UserIdentity links one OAuth/OIDC provider identity to a user.
type UserIdentity struct {
	Base
	UserID         uuid.UUID `gorm:"type:uuid;not null;index;uniqueIndex:ux_identity_provider"`
	Provider       string    `gorm:"size:64;not null;uniqueIndex:ux_identity_provider"`
	ProviderUserID string    `gorm:"size:255;not null;uniqueIndex:ux_identity_provider"`
}
