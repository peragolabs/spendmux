package models

import "github.com/google/uuid"

// TenantCap is a per-customer hard stop on the account's BYOK spend.
// One tenant hitting the cap must not take down other tenants or the app.
type TenantCap struct {
	Base
	UserID           uuid.UUID `gorm:"type:uuid;uniqueIndex:idx_user_tenant" json:"user_id"`
	Tenant           string    `gorm:"size:200;uniqueIndex:idx_user_tenant" json:"tenant"`
	MonthlyUSDMicros int64     `json:"monthly_usd_micros"`
}
