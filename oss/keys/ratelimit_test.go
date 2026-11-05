package keys

import (
	"errors"
	"testing"
	"time"
)

func TestAPIKeyRateLimitConfig(t *testing.T) {
	t.Setenv("SPENDMUX_API_RATE_LIMIT_REQUESTS", "75")
	t.Setenv("SPENDMUX_API_RATE_LIMIT_WINDOW_SECONDS", "120")
	limit, window := apiKeyRateLimitConfig()
	if limit != 75 || window != 2*time.Minute {
		t.Fatalf("got limit=%d window=%s", limit, window)
	}
}

func TestAPIKeyRateLimitConfigAllowsDisable(t *testing.T) {
	t.Setenv("SPENDMUX_API_RATE_LIMIT_REQUESTS", "0")
	limit, _ := apiKeyRateLimitConfig()
	if limit != 0 {
		t.Fatalf("expected disabled limit, got %d", limit)
	}
}

func TestAPIKeyRateLimitRetryAfter(t *testing.T) {
	err := &PlatformKeyRateLimitError{RetryAfter: 1500 * time.Millisecond}
	if !errors.Is(err, ErrPlatformKeyRateLimit) {
		t.Fatal("typed error should unwrap to ErrPlatformKeyRateLimit")
	}
	if got := APIKeyRateLimitRetryAfter(err); got != 2 {
		t.Fatalf("expected 2 seconds, got %d", got)
	}
}
