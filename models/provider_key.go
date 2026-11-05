package models

import (
	"time"

	"github.com/google/uuid"
)

// ProviderKey stores an encrypted upstream API key for a user + provider.
// A user may save several keys for one provider; requests are spread across
// them by Weight and a key that is rate-limited or failing cools down.
type ProviderKey struct {
	Base
	UserID       uuid.UUID `gorm:"type:uuid;index;index:idx_user_provider" json:"user_id"`
	Provider     string    `gorm:"size:50;index:idx_user_provider" json:"provider"` // openai, groq, ...
	EncryptedKey string    `gorm:"type:text" json:"-"`
	KeyHint      string    `gorm:"size:20" json:"key_hint"` // last 4 chars for UI
	Label        string    `gorm:"size:100" json:"label"`
	// BaseURLOverride optional endpoint (Azure resource URL, Cloudflare account AI URL, LAN Ollama, etc.)
	BaseURLOverride string `gorm:"size:500" json:"base_url_override,omitempty"`

	// Weight is this key's share of traffic among the provider's keys (1-100).
	Weight int `gorm:"not null;default:1" json:"weight"`
	// CooldownUntil set = skip this key until then unless every key is cooling down.
	CooldownUntil *time.Time `json:"cooldown_until,omitempty"`
	// LastError is the most recent upstream failure on this key (status and reason), for the Keys page.
	LastError   string     `gorm:"size:300" json:"last_error,omitempty"`
	LastErrorAt *time.Time `json:"last_error_at,omitempty"`
}

// PlatformAPIKey authenticates calls to /v1/* (your proxy key, not the provider key).
// Optional scopes limit which upstream provider/model the bearer may call.
// Optional token limits are hard stops, enforced before the provider is called.
type PlatformAPIKey struct {
	Base
	UserID     uuid.UUID  `gorm:"type:uuid;index" json:"user_id"`
	Name       string     `gorm:"size:100" json:"name"`
	KeyHash    string     `gorm:"size:64;uniqueIndex" json:"-"`
	Prefix     string     `gorm:"size:16" json:"prefix"` // e.g. sk-abc1...
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`

	// AllowedProvider empty = any provider. When set, requests must use this provider.
	AllowedProvider string `gorm:"size:50" json:"allowed_provider,omitempty"`
	// AllowedModel empty = any model (within AllowedProvider if set).
	AllowedModel string `gorm:"size:150" json:"allowed_model,omitempty"`

	// DailyTokenLimit / WeeklyTokenLimit: 0 = unlimited. Hard stop when SUM(total_tokens) in window >= limit.
	DailyTokenLimit  int64 `json:"daily_token_limit"`
	WeeklyTokenLimit int64 `json:"weekly_token_limit"`

	// USD caps in microdollars (1e-6 USD). 0 = unlimited. Enforced before egress.
	DailyUSDMicros   int64 `json:"daily_usd_micros"`
	WeeklyUSDMicros  int64 `json:"weekly_usd_micros"`
	MonthlyUSDMicros int64 `json:"monthly_usd_micros"`
	SessionUSDMicros int64 `json:"session_usd_micros"`

	// RPMLimit / TPMLimit: 0 = fall back to process env (RPM) or unlimited (TPM).
	RPMLimit int64 `json:"rpm_limit"`
	TPMLimit int64 `json:"tpm_limit"`

	// CacheTTLSeconds > 0 caches this token's replies by default for that long.
	// The X-Spendmux-Cache header overrides it per request.
	CacheTTLSeconds int64 `gorm:"not null;default:0" json:"cache_ttl_seconds"`
	// AutoCacheOff turns off the savings Spendmux applies by default: prompt
	// cache breakpoints on Anthropic calls and the embeddings response cache.
	AutoCacheOff bool `gorm:"not null;default:false" json:"auto_cache_off"`

	// PausedAt set = kill switch on: every call is refused (403 key_paused)
	// until resumed. Unlike delete, the token, its caps and its memory survive.
	PausedAt *time.Time `json:"paused_at,omitempty"`
	// ExpiresAt set = the token stops working at that instant (401 key_expired).
	ExpiresAt *time.Time `gorm:"index" json:"expires_at,omitempty"`
}
