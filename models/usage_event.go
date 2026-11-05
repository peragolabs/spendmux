package models

import "github.com/google/uuid"

// UsageEvent is one proxied request for the spend dashboard / audit trail.
type UsageEvent struct {
	Base
	ReservationID     *uuid.UUID `gorm:"type:uuid;index" json:"-"`
	UserID            uuid.UUID  `gorm:"type:uuid;index" json:"user_id"`
	PlatformAPIKeyID  *uuid.UUID `gorm:"type:uuid;index" json:"platform_api_key_id,omitempty"`
	Provider          string     `gorm:"size:50;index" json:"provider"`
	Model             string     `gorm:"size:150;index" json:"model"`
	Endpoint          string     `gorm:"size:50" json:"endpoint"` // chat.completions | embeddings
	PromptTokens      int        `json:"prompt_tokens"`
	CompletionTokens  int        `json:"completion_tokens"`
	TotalTokens       int        `json:"total_tokens"`
	CostUSDMicros     int64      `json:"cost_usd_micros"` // microdollars (1e-6 USD); list price, markup 0
	ListCostUSDMicros int64      `json:"list_cost_usd_micros"`
	LatencyMs         int64      `json:"latency_ms"`
	TTFTMs            int64      `json:"ttft_ms"`
	StatusCode        int        `json:"status_code"`
	Streamed          bool       `json:"streamed"`
	ErrorMessage      string     `gorm:"size:500" json:"error_message,omitempty"`
	SessionID         string     `gorm:"size:200;index" json:"session_id,omitempty"`
	TenantID          string     `gorm:"size:200;index" json:"tenant_id,omitempty"`
	CachedTokens      int        `json:"cached_tokens"`
	ReasoningTokens   int        `json:"reasoning_tokens"`
	// CacheWriteTokens are prompt tokens the provider wrote to its prompt
	// cache (Anthropic bills them at a premium); CachedTokens are ones read back.
	CacheWriteTokens int `gorm:"not null;default:0" json:"cache_write_tokens"`
	// CacheHit marks a reply served from the response cache: CostUSDMicros is
	// 0 and CostSavedUSDMicros is what the original call cost. On other rows
	// CostSavedUSDMicros is what provider prompt caching took off the bill.
	CacheHit           bool  `gorm:"not null;default:false" json:"cache_hit"`
	CostSavedUSDMicros int64 `gorm:"not null;default:0" json:"cost_saved_usd_micros"`
	// RequestHash fingerprints the request (keyed HMAC, not reversible) so
	// identical repeats can be counted; it is the same for byte-identical asks.
	RequestHash string `gorm:"size:64;not null;default:''" json:"-"`
}
