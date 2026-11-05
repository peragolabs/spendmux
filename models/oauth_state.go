package models

import "time"

// OAuthState is a pending sign-in: the sha256 of the state parameter sent to
// the identity provider, consumed once on callback. Rows expire after five
// minutes.
type OAuthState struct {
	ID         string    `gorm:"primaryKey;size:64"`
	Provider   string    `gorm:"size:50;not null"`
	LinkUserID string    `gorm:"size:64"`
	CreatedAt  time.Time `gorm:"index;not null"`
}
