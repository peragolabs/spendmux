package keys

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/peragolabs/spendmux/internal/database"
)

const (
	defaultLoopMax           = int64(25)
	defaultLoopWindowSeconds = int64(300)
)

var ErrLoopDetected = errors.New("identical request loop detected")

// HashLoopRequest fingerprints model + messages so retry loops can be stopped
// before they hit the provider. Extra request fields are ignored on purpose:
// the detector is about the same prompt, not temperature jitter.
func HashLoopRequest(model string, messages any) string {
	payload, err := json.Marshal(struct {
		Model    string `json:"model"`
		Messages any    `json:"messages"`
	}{Model: model, Messages: messages})
	if err != nil {
		sum := sha256.Sum256([]byte(model))
		return hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// EnforceLoopDetector 429s when the same fingerprint repeats too often on one
// virtual key. A max of 0 disables detection.
func EnforceLoopDetector(keyID uuid.UUID, fingerprint string) error {
	max, window := loopConfig()
	if max <= 0 || keyID == uuid.Nil || fingerprint == "" {
		return nil
	}
	now := time.Now().UTC()
	windowStart := now.Truncate(window)
	var row struct {
		RequestCount    int64
		WindowStartedAt time.Time
	}
	err := database.Get().Raw(`
		INSERT INTO loop_fingerprint_buckets
			(platform_api_key_id, fingerprint, window_started_at, request_count)
		VALUES (?, ?, ?, 1)
		ON CONFLICT (platform_api_key_id, fingerprint) DO UPDATE SET
			window_started_at = CASE
				WHEN loop_fingerprint_buckets.window_started_at < EXCLUDED.window_started_at
				THEN EXCLUDED.window_started_at
				ELSE loop_fingerprint_buckets.window_started_at
			END,
			request_count = CASE
				WHEN loop_fingerprint_buckets.window_started_at < EXCLUDED.window_started_at
				THEN 1
				ELSE loop_fingerprint_buckets.request_count + 1
			END
		RETURNING request_count, window_started_at
	`, keyID, fingerprint, windowStart).Scan(&row).Error
	if err != nil {
		return fmt.Errorf("enforce loop detector: %w", err)
	}
	if row.RequestCount > max {
		return fmt.Errorf("%w: %d identical calls in %s", ErrLoopDetected, row.RequestCount, window)
	}
	return nil
}

func loopConfig() (int64, time.Duration) {
	max := envInt64("SPENDMUX_LOOP_MAX", defaultLoopMax)
	windowSeconds := envInt64("SPENDMUX_LOOP_WINDOW_SECONDS", defaultLoopWindowSeconds)
	if windowSeconds < 1 {
		windowSeconds = defaultLoopWindowSeconds
	}
	return max, time.Duration(windowSeconds) * time.Second
}

// PurgeStaleSpendState removes rows nothing reads any more: loop buckets
// whose window ended (a new request starts a fresh window anyway) and
// reservations soft-deleted by older releases. Batched so a large backlog
// never holds long locks; the maintenance loop calls it repeatedly.
func PurgeStaleSpendState() (int64, error) {
	_, window := loopConfig()
	cutoff := time.Now().UTC().Add(-2 * window)
	db := database.Get()
	var total int64
	res := db.Exec(`DELETE FROM loop_fingerprint_buckets WHERE ctid IN (
		SELECT ctid FROM loop_fingerprint_buckets WHERE window_started_at < ? LIMIT 5000)`, cutoff)
	if res.Error != nil {
		return total, res.Error
	}
	total += res.RowsAffected
	res = db.Exec(`DELETE FROM token_reservations WHERE ctid IN (
		SELECT ctid FROM token_reservations WHERE deleted_at IS NOT NULL LIMIT 5000)`)
	if res.Error != nil {
		return total, res.Error
	}
	return total + res.RowsAffected, nil
}
