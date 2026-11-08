package proxy

import (
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	appcrypto "github.com/peragolabs/spendmux/internal/crypto"
	"github.com/peragolabs/spendmux/internal/database"
	"github.com/peragolabs/spendmux/internal/providers"
	"github.com/peragolabs/spendmux/internal/upstream"
	"github.com/peragolabs/spendmux/models"
)

// A user can save several keys for one provider. Each request walks them in
// weighted-random order, skipping keys that are cooling down after a 429, an
// auth failure or a 5xx, and retries the next key after a short jittered
// backoff. This stays inside one provider and model, so it needs no opt-in;
// switching providers is still only done by "fallback": true.

// keyCandidate is one saved key, decrypted only when it is about to be used.
type keyCandidate struct {
	row models.ProviderKey
}

// loadKeyCandidates returns the user's keys for cfg in the order to try them:
// healthy keys shuffled by weight, then cooling keys soonest-ready first, so a
// provider whose keys are all cooling still gets one attempt.
func loadKeyCandidates(userID uuid.UUID, cfg providers.Config) ([]keyCandidate, error) {
	var rows []models.ProviderKey
	if err := database.Get().Where("user_id = ? AND provider = ?", userID, cfg.ID).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to load provider keys")
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no key saved for provider '%s' — add it at /keys", cfg.ID)
	}
	return orderKeys(rows, time.Now(), rand.Float64), nil // #nosec G404 -- load spreading, not a secret
}

func orderKeys(rows []models.ProviderKey, now time.Time, random func() float64) []keyCandidate {
	type scored struct {
		row   models.ProviderKey
		score float64
	}
	var healthy, cooling []scored
	for _, r := range rows {
		if r.CooldownUntil != nil && r.CooldownUntil.After(now) {
			cooling = append(cooling, scored{row: r})
			continue
		}
		// Efraimidis-Spirakis: sorting by u^(1/w) descending is a weighted shuffle.
		w := float64(clampWeight(r.Weight))
		healthy = append(healthy, scored{row: r, score: math.Pow(random(), 1/w)})
	}
	sort.SliceStable(healthy, func(i, j int) bool { return healthy[i].score > healthy[j].score })
	sort.SliceStable(cooling, func(i, j int) bool { return cooling[i].row.CooldownUntil.Before(*cooling[j].row.CooldownUntil) })
	out := make([]keyCandidate, 0, len(rows))
	for _, s := range healthy {
		out = append(out, keyCandidate{row: s.row})
	}
	for _, s := range cooling {
		out = append(out, keyCandidate{row: s.row})
	}
	return out
}

func clampWeight(w int) int {
	switch {
	case w < 1:
		return 1
	case w > 100:
		return 100
	}
	return w
}

// maxKeyAttempts caps how many keys one hop tries (PROVIDER_KEY_MAX_ATTEMPTS, default 3).
func maxKeyAttempts(n int) int {
	limit := 3
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("PROVIDER_KEY_MAX_ATTEMPTS"))); err == nil && v > 0 {
		limit = v
	}
	if limit > 8 {
		limit = 8
	}
	if n < limit {
		return n
	}
	return limit
}

// open decrypts the key and returns cfg with this key's endpoint applied
// (base URL override, Bedrock region, Vertex project).
func (k keyCandidate) open(cfg providers.Config) (providers.Config, string, error) {
	apiKey, err := appcrypto.Decrypt(k.row.EncryptedKey)
	if err != nil {
		return cfg, "", fmt.Errorf("failed to decrypt provider key %s", k.row.KeyHint)
	}
	switch cfg.AuthStyle {
	case "aws-sigv4":
		creds, err := providers.ParseAWSCreds(apiKey)
		if err != nil {
			return cfg, "", err
		}
		cfg.BaseURL = providers.BedrockOpenAIBaseURL(creds.Region)
	case "vertex":
		creds, err := providers.ParseVertexCreds(apiKey)
		if err != nil {
			return cfg, "", err
		}
		cfg.BaseURL = providers.VertexOpenAIBaseURL(creds.Project, creds.Location)
	}
	if override := strings.TrimSpace(k.row.BaseURLOverride); override != "" {
		cfg.BaseURL = override
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return cfg, "", fmt.Errorf("provider '%s' needs a base URL — set it on /keys (Azure resource URL, Cloudflare AI URL, etc.)", cfg.ID)
	}
	if err := upstream.ValidateURL(cfg.BaseURL); err != nil {
		return cfg, "", fmt.Errorf("invalid upstream URL for provider '%s': %w", cfg.ID, err)
	}
	return cfg, apiKey, nil
}

// keyRetryable reports whether another key of the same provider could succeed
// where this one failed: rate limits, auth failures, timeouts and 5xx.
func keyRetryable(status int) bool {
	switch {
	case status == http.StatusTooManyRequests, status == http.StatusUnauthorized,
		status == http.StatusForbidden, status == http.StatusRequestTimeout:
		return true
	case status >= 500 || status == 0:
		return true
	}
	return false
}

// cooldownFor is how long a key sits out after failing with status.
// Retry-After (seconds or HTTP date) wins for 429 and 503, capped at 10 minutes.
func cooldownFor(status int, retryAfter string, now time.Time) time.Duration {
	const maxCooldown = 10 * time.Minute
	if status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
		if d, ok := parseRetryAfter(retryAfter, now); ok {
			return min(max(d, time.Second), maxCooldown)
		}
	}
	switch {
	case status == http.StatusTooManyRequests:
		return time.Minute
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return maxCooldown // a revoked or wrong key will not fix itself in seconds
	default:
		return 30 * time.Second
	}
}

func parseRetryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.ParseFloat(v, 64); err == nil && secs >= 0 {
		return time.Duration(secs * float64(time.Second)), true
	}
	if t, err := http.ParseTime(v); err == nil {
		return t.Sub(now), true
	}
	return 0, false
}

// failed records a failure. When another key could do better it cools this
// key down and returns true so the caller tries the next one.
func (k keyCandidate) failed(status int, retryAfter, reason string) bool {
	if !keyRetryable(status) {
		return false
	}
	now := time.Now()
	until := now.Add(cooldownFor(status, retryAfter, now))
	msg := strconv.Itoa(status)
	if status == 0 {
		msg = "network"
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		msg += ": " + reason
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	database.Get().Model(&models.ProviderKey{}).Where("id = ?", k.row.ID).
		UpdateColumns(map[string]interface{}{"cooldown_until": until, "last_error": msg, "last_error_at": now})
	return true
}

// succeeded clears a cooldown or error left by an earlier failure.
// Healthy keys cost no write.
func (k keyCandidate) succeeded() {
	if k.row.CooldownUntil == nil && k.row.LastError == "" {
		return
	}
	database.Get().Model(&models.ProviderKey{}).Where("id = ?", k.row.ID).
		UpdateColumns(map[string]interface{}{"cooldown_until": nil, "last_error": ""})
}

// keyAttempt skips upstream's same-key retry when another key can take the call.
func keyAttempt(r *http.Request, moreKeys bool) *http.Request {
	if moreKeys {
		return upstream.WithoutStatusRetry(r)
	}
	return r
}

// keyBackoff sleeps before the next key: full jitter over 100ms·2^attempt, capped at 1s.
func keyBackoff(attempt int) {
	ceiling := min(100*time.Millisecond<<attempt, time.Second)
	time.Sleep(time.Duration(rand.Int64N(int64(ceiling) + 1))) // #nosec G404 -- jitter, not a secret
}
