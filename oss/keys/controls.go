package keys

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/vrannang1/spendmux/internal/auth"
	"github.com/vrannang1/spendmux/internal/database"
	"github.com/vrannang1/spendmux/models"
)

// Token kill switches and edits. Pausing refuses every call on a token at
// once without losing it, so an app can be stopped and started again with
// the same credential; editing changes caps without re-minting and
// redeploying the app.

// tokenStatus is "paused", "expired" or "active".
func tokenStatus(k models.PlatformAPIKey, now time.Time) string {
	switch {
	case k.PausedAt != nil:
		return "paused"
	case k.ExpiresAt != nil && !now.Before(*k.ExpiresAt):
		return "expired"
	}
	return "active"
}

// parseExpiryForm reads an optional YYYY-MM-DD expiry. The token works
// through the end of that UTC day. Blank means no expiry.
func parseExpiryForm(v string) (*time.Time, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, true
	}
	d, err := time.Parse("2006-01-02", v)
	if err != nil {
		return nil, false
	}
	end := d.UTC().AddDate(0, 0, 1)
	if !end.After(time.Now().UTC()) {
		return nil, false
	}
	return &end, true
}

func formatDate(t *time.Time) string {
	if t == nil {
		return ""
	}
	// Stored as the exclusive end of the chosen day; show the day itself.
	return t.UTC().Add(-time.Nanosecond).Format("2006-01-02")
}

func ownedToken(c *fiber.Ctx) (uuid.UUID, models.PlatformAPIKey, bool) {
	userID, err := auth.GetUserID(c)
	if err != nil || userID == uuid.Nil {
		return uuid.Nil, models.PlatformAPIKey{}, false
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return userID, models.PlatformAPIKey{}, false
	}
	var k models.PlatformAPIKey
	if err := database.Get().Where("id = ? AND user_id = ?", id, userID).First(&k).Error; err != nil {
		return userID, models.PlatformAPIKey{}, false
	}
	return userID, k, true
}

// PausePlatformKeyHandler is the kill switch: POST /keys/platform/:id/pause
// with paused=1 refuses every call on the token; paused=0 resumes it.
func PausePlatformKeyHandler(c *fiber.Ctx) error {
	userID, k, ok := ownedToken(c)
	if userID == uuid.Nil {
		return c.Redirect("/auth")
	}
	if !ok {
		return finish(c, "/keys?error=token_not_found#tokens")
	}
	var pausedAt *time.Time
	if c.FormValue("paused") != "0" {
		now := time.Now().UTC()
		pausedAt = &now
	}
	if err := database.Get().Model(&k).Update("paused_at", pausedAt).Error; err != nil {
		return finish(c, "/keys?error=save_failed#tokens")
	}
	return finish(c, "/keys#tokens")
}

// UpdatePlatformKeyHandler edits a token's name, caps, limits and expiry in
// place: POST /keys/platform/:id/update. Every field is sent; blank caps
// mean unlimited, matching the mint form.
func UpdatePlatformKeyHandler(c *fiber.Ctx) error {
	userID, k, ok := ownedToken(c)
	if userID == uuid.Nil {
		return c.Redirect("/auth")
	}
	if !ok {
		return finish(c, "/keys?error=token_not_found#tokens")
	}
	for _, field := range []string{"daily_usd_cap", "weekly_usd_cap", "monthly_usd_cap", "session_usd_cap"} {
		if DollarsToMicros(c.FormValue(field)) < 0 {
			return c.Status(400).JSON(fiber.Map{"error": "Invalid dollar cap: " + field})
		}
	}
	expires, ok := parseExpiryForm(c.FormValue("expires_at"))
	if !ok {
		return finish(c, "/keys?error=bad_expiry#tokens")
	}
	name := strings.TrimSpace(c.FormValue("name"))
	if name == "" {
		name = k.Name
	}
	err := database.Get().Model(&k).Select(
		"name", "daily_usd_micros", "weekly_usd_micros", "monthly_usd_micros", "session_usd_micros",
		"daily_token_limit", "weekly_token_limit", "rpm_limit", "tpm_limit", "cache_ttl_seconds", "auto_cache_off", "expires_at",
	).Updates(models.PlatformAPIKey{
		Name:             name,
		DailyUSDMicros:   DollarsToMicros(c.FormValue("daily_usd_cap")),
		WeeklyUSDMicros:  DollarsToMicros(c.FormValue("weekly_usd_cap")),
		MonthlyUSDMicros: DollarsToMicros(c.FormValue("monthly_usd_cap")),
		SessionUSDMicros: DollarsToMicros(c.FormValue("session_usd_cap")),
		DailyTokenLimit:  parseInt64Form(c.FormValue("daily_token_limit")),
		WeeklyTokenLimit: parseInt64Form(c.FormValue("weekly_token_limit")),
		RPMLimit:         parseInt64Form(c.FormValue("rpm_limit")),
		TPMLimit:         parseInt64Form(c.FormValue("tpm_limit")),
		CacheTTLSeconds:  parseCacheTTL(c.FormValue("cache_ttl")),
		AutoCacheOff:     c.FormValue("auto_cache") == "off",
		ExpiresAt:        expires,
	}).Error
	if err != nil {
		return finish(c, "/keys?error=save_failed#tokens")
	}
	return finish(c, "/keys#tokens")
}
