package keys

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vrannang1/spendmux/internal/database"
	"github.com/vrannang1/spendmux/models"
)

const (
	defaultAPIKeyRateLimitRequests = int64(30)
	defaultAPIKeyRateLimitWindow   = 60 * time.Second
)

var ErrPlatformKeyRateLimit = errors.New("platform API key rate limit exceeded")

type PlatformKeyRateLimitError struct {
	RetryAfter time.Duration
}

func (e *PlatformKeyRateLimitError) Error() string { return ErrPlatformKeyRateLimit.Error() }
func (e *PlatformKeyRateLimitError) Unwrap() error { return ErrPlatformKeyRateLimit }

func APIKeyRateLimitRetryAfter(err error) int {
	var limitErr *PlatformKeyRateLimitError
	if !errors.As(err, &limitErr) {
		return 0
	}
	seconds := int(limitErr.RetryAfter.Round(time.Second) / time.Second)
	if seconds < 1 {
		return 1
	}
	return seconds
}

// EnforcePlatformKeyRateLimit counts requests for a virtual key using the
// process-wide default RPM. Prefer EnforceWalletRateLimits when a wallet
// override or TPM cap is in play.
func EnforcePlatformKeyRateLimit(keyID uuid.UUID) error {
	return EnforceWalletRateLimits(models.PlatformAPIKey{Base: models.Base{ID: keyID}}, 0)
}

// EnforceWalletRateLimits increments the per-wallet RPM/TPM window. RPM of 0
// falls back to SPENDMUX_API_RATE_LIMIT_REQUESTS; TPM of 0 is unlimited.
func EnforceWalletRateLimits(pk models.PlatformAPIKey, tokens int64) error {
	if pk.ID == uuid.Nil {
		return nil
	}
	rpm, window := walletRPM(pk)
	tpm := pk.TPMLimit
	if rpm <= 0 && tpm <= 0 {
		return nil
	}
	if tokens < 0 {
		tokens = 0
	}
	now := time.Now().UTC()
	windowStart := now.Truncate(window)
	var row struct {
		RequestCount    int64
		TokenCount      int64
		WindowStartedAt time.Time
	}
	err := database.Get().Raw(`
		INSERT INTO platform_key_rate_limit_buckets
			(platform_api_key_id, window_started_at, request_count, token_count)
		VALUES (?, ?, 1, ?)
		ON CONFLICT (platform_api_key_id) DO UPDATE SET
			window_started_at = CASE
				WHEN platform_key_rate_limit_buckets.window_started_at < EXCLUDED.window_started_at
				THEN EXCLUDED.window_started_at
				ELSE platform_key_rate_limit_buckets.window_started_at
			END,
			request_count = CASE
				WHEN platform_key_rate_limit_buckets.window_started_at < EXCLUDED.window_started_at
				THEN 1
				ELSE platform_key_rate_limit_buckets.request_count + 1
			END,
			token_count = CASE
				WHEN platform_key_rate_limit_buckets.window_started_at < EXCLUDED.window_started_at
				THEN EXCLUDED.token_count
				ELSE platform_key_rate_limit_buckets.token_count + EXCLUDED.token_count
			END
		RETURNING request_count, token_count, window_started_at
	`, pk.ID, windowStart, tokens).Scan(&row).Error
	if err != nil {
		return fmt.Errorf("enforce platform API key rate limit: %w", err)
	}
	if rpm > 0 && row.RequestCount > rpm {
		remaining := row.WindowStartedAt.Add(window).Sub(now)
		if remaining <= 0 {
			remaining = time.Second
		}
		return &PlatformKeyRateLimitError{RetryAfter: remaining}
	}
	if tpm > 0 && row.TokenCount > tpm {
		remaining := row.WindowStartedAt.Add(window).Sub(now)
		if remaining <= 0 {
			remaining = time.Second
		}
		return fmt.Errorf("%w", ErrTPMLimit)
	}
	return nil
}

func walletRPM(pk models.PlatformAPIKey) (int64, time.Duration) {
	if pk.RPMLimit > 0 {
		return pk.RPMLimit, time.Minute
	}
	return apiKeyRateLimitConfig()
}

func apiKeyRateLimitConfig() (int64, time.Duration) {
	limit := envInt64("SPENDMUX_API_RATE_LIMIT_REQUESTS", defaultAPIKeyRateLimitRequests)
	windowSeconds := envInt64("SPENDMUX_API_RATE_LIMIT_WINDOW_SECONDS", int64(defaultAPIKeyRateLimitWindow/time.Second))
	if windowSeconds < 1 {
		windowSeconds = int64(defaultAPIKeyRateLimitWindow / time.Second)
	}
	return limit, time.Duration(windowSeconds) * time.Second
}

func envInt64(name string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fallback
	}
	return value
}
