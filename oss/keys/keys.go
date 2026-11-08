package keys

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/peragolabs/spendmux/internal/auth"
	"github.com/peragolabs/spendmux/internal/core"
	appcrypto "github.com/peragolabs/spendmux/internal/crypto"
	"github.com/peragolabs/spendmux/internal/database"
	"github.com/peragolabs/spendmux/internal/providers"
	"github.com/peragolabs/spendmux/models"
	"github.com/peragolabs/spendmux/oss/memory"
)

// MintPlatformAPIKey creates a hashed bearer token for the user. Returns plaintext once.
func MintPlatformAPIKey(userID uuid.UUID, name string) (plain string, err error) {
	return MintWallet(userID, WalletSpec{Name: name})
}

// WalletSpec is the mint form for a virtual key (wallet) on the shared BYOK vault.
type WalletSpec struct {
	Name             string
	AllowedProvider  string
	AllowedModel     string
	DailyTokenLimit  int64
	WeeklyTokenLimit int64
	DailyUSDMicros   int64
	WeeklyUSDMicros  int64
	MonthlyUSDMicros int64
	SessionUSDMicros int64
	RPMLimit         int64
	TPMLimit         int64
	CacheTTLSeconds  int64
	AutoCacheOff     bool
	ExpiresAt        *time.Time
}

// Errors from ResolvePlatformKey for a token that exists but may not be used.
var (
	ErrKeyPaused  = errors.New("this token is paused — resume it on /keys")
	ErrKeyExpired = errors.New("this token has expired — mint a new one on /keys")
)

// MintPlatformAPIKeyScoped creates a bearer with optional provider/model scope and token budgets.
func MintPlatformAPIKeyScoped(userID uuid.UUID, name, allowedProvider, allowedModel string, dailyLimit, weeklyLimit int64) (plain string, err error) {
	return MintWallet(userID, WalletSpec{
		Name:             name,
		AllowedProvider:  allowedProvider,
		AllowedModel:     allowedModel,
		DailyTokenLimit:  dailyLimit,
		WeeklyTokenLimit: weeklyLimit,
	})
}

// MintWallet creates a hashed bearer token with USD, session, and rate-limit caps.
func MintWallet(userID uuid.UUID, spec WalletSpec) (plain string, err error) {
	if spec.Name == "" {
		spec.Name = "Untitled"
	}
	plain, err = appcrypto.NewPlatformAPIKey()
	if err != nil {
		return "", err
	}
	prefix := plain
	if len(prefix) > 12 {
		prefix = prefix[:12] + "…"
	}
	if err := database.Get().Create(&models.PlatformAPIKey{
		UserID:           userID,
		Name:             spec.Name,
		KeyHash:          appcrypto.HashAPIKey(plain),
		Prefix:           prefix,
		AllowedProvider:  strings.ToLower(strings.TrimSpace(spec.AllowedProvider)),
		AllowedModel:     strings.TrimSpace(spec.AllowedModel),
		DailyTokenLimit:  spec.DailyTokenLimit,
		WeeklyTokenLimit: spec.WeeklyTokenLimit,
		DailyUSDMicros:   spec.DailyUSDMicros,
		WeeklyUSDMicros:  spec.WeeklyUSDMicros,
		MonthlyUSDMicros: spec.MonthlyUSDMicros,
		SessionUSDMicros: spec.SessionUSDMicros,
		RPMLimit:         spec.RPMLimit,
		TPMLimit:         spec.TPMLimit,
		CacheTTLSeconds:  spec.CacheTTLSeconds,
		AutoCacheOff:     spec.AutoCacheOff,
		ExpiresAt:        spec.ExpiresAt,
	}).Error; err != nil {
		return "", err
	}
	return plain, nil
}

// KeysPageHandler renders BYOK settings + platform API key management.
func KeysPageHandler(c *fiber.Ctx) error {
	userID, err := auth.GetUserID(c)
	if err != nil || userID == uuid.Nil {
		return c.Redirect("/auth")
	}

	db := database.Get()
	var user models.User
	if err := db.First(&user, "id = ?", userID).Error; err != nil {
		return c.Redirect("/")
	}

	var keys []models.ProviderKey
	db.Where("user_id = ?", userID).Order("provider asc").Find(&keys)

	var platformKeys []models.PlatformAPIKey
	db.Where("user_id = ?", userID).Order("created_at desc").Find(&platformKeys)

	type keyRow struct {
		// Sealed: encrypted in the browser with the vault public key (v2 envelope).
		Sealed   bool
		ID       string
		Provider string
		Name     string
		Hint     string
		Label    string
		BaseURL  string
		Weight   int
		// Cooling is "HH:MM UTC" while the key sits out after failures, else "".
		Cooling   string
		LastError string
	}
	rows := make([]keyRow, 0, len(keys))
	now := time.Now()
	for _, k := range keys {
		name := k.Provider
		if p, ok := providers.Get(k.Provider); ok {
			name = p.Name
		}
		rows = append(rows, keyRow{
			ID:        k.ID.String(),
			Provider:  k.Provider,
			Name:      name,
			Hint:      k.KeyHint,
			Label:     k.Label,
			BaseURL:   k.BaseURLOverride,
			Sealed:    appcrypto.IsSealed(k.EncryptedKey),
			Weight:    max(k.Weight, 1),
			Cooling:   coolingLabel(k.CooldownUntil, now),
			LastError: k.LastError,
		})
	}

	type platRow struct {
		ID          string
		Name        string
		Prefix      string
		Scope       string
		DailyLimit  int64
		WeeklyLimit int64
		DayUsed     int64
		WeekUsed    int64
		DailyUSD    string
		WeeklyUSD   string
		MonthlyUSD  string
		SessionUSD  string
		DayUSDUsed  string
		WeekUSDUsed string
		RPM         int64
		TPM         int64
		CacheTTL    int64
		AutoCache   bool
		// Summary is a human line like "Day $0.01 used · Week $0.01 used · Session cap $0.01 · no other caps".
		Summary string
		// Status is "active", "paused" or "expired"; ExpiresAt is YYYY-MM-DD (UTC) or "".
		Status    string
		ExpiresAt string
	}
	plats := make([]platRow, 0, len(platformKeys))
	for _, k := range platformKeys {
		scope := "any provider / model"
		if k.AllowedProvider != "" {
			scope = k.AllowedProvider
			if k.AllowedModel != "" {
				scope += " / " + k.AllowedModel
			} else {
				scope += " / any model"
			}
		}
		dayUsed, weekUsed := sumKeyTokens(k.ID)
		dayUSD, weekUSD := sumKeyUSD(k.ID)
		plats = append(plats, platRow{
			ID:          k.ID.String(),
			Name:        k.Name,
			Prefix:      k.Prefix,
			Scope:       scope,
			DailyLimit:  k.DailyTokenLimit,
			WeeklyLimit: k.WeeklyTokenLimit,
			DayUsed:     dayUsed,
			WeekUsed:    weekUsed,
			DailyUSD:    MicrosToDollars(k.DailyUSDMicros),
			WeeklyUSD:   MicrosToDollars(k.WeeklyUSDMicros),
			MonthlyUSD:  MicrosToDollars(k.MonthlyUSDMicros),
			SessionUSD:  MicrosToDollars(k.SessionUSDMicros),
			DayUSDUsed:  MicrosToDollars(dayUSD),
			WeekUSDUsed: MicrosToDollars(weekUSD),
			RPM:         k.RPMLimit,
			TPM:         k.TPMLimit,
			CacheTTL:    k.CacheTTLSeconds,
			AutoCache:   !k.AutoCacheOff,
			Summary:     tokenSummary(k, dayUsed, weekUsed, dayUSD, weekUSD),
			Status:      tokenStatus(k, time.Now()),
			ExpiresAt:   formatDate(k.ExpiresAt),
		})
	}

	flashKey := c.Query("new_key")
	flashErr := keysFlash(c.Query("error"))

	tenants, _ := ListTenantCaps(userID)
	type tenantRow struct {
		Tenant string
		Cap    string
	}
	tenantRows := make([]tenantRow, 0, len(tenants))
	for _, t := range tenants {
		tenantRows = append(tenantRows, tenantRow{Tenant: t.Tenant, Cap: MicrosToDollars(t.MonthlyUSDMicros)})
	}

	return c.JSON(fiber.Map{
		"User":           fiber.Map{"name": user.Name, "email": user.Email},
		"ProviderGroups": providers.Groups(),
		"ProviderCount":  len(providers.All()),
		"ProviderKeys":   rows,
		"PlatformKeys":   plats,
		"TenantCaps":     tenantRows,
		"NewPlatformKey": flashKey,
		"Error":          flashErr,
		"BaseURL":        core.PublicAPIBase(c),
	})
}

// SaveProviderKeyHandler upserts an encrypted provider key.
func SaveProviderKeyHandler(c *fiber.Ctx) error {
	userID, err := auth.GetUserID(c)
	if err != nil || userID == uuid.Nil {
		return c.Redirect("/auth")
	}

	provider := strings.ToLower(strings.TrimSpace(c.FormValue("provider")))
	apiKey := strings.TrimSpace(c.FormValue("api_key"))
	label := strings.TrimSpace(c.FormValue("label"))
	baseOverride := strings.TrimSpace(c.FormValue("base_url"))
	weight, ok := parseWeight(c.FormValue("weight"))
	if !ok {
		return finish(c, "/keys?error=bad_weight#providers")
	}

	cfg, ok := providers.Get(provider)
	if !ok {
		return finish(c, "/keys?error=unknown_provider#providers")
	}

	var plaintext string
	var hintSrc string
	var enc string

	// Browser-sealed path: the client already encrypted the credential (for
	// aws/vertex, the same JSON MarshalAWSCreds/MarshalVertexCreds would
	// build). The server validates the envelope's shape and key id, adds
	// its own encryption layer, and never sees the plaintext.
	if envelope := strings.TrimSpace(c.FormValue("envelope")); envelope != "" {
		sealed, err := appcrypto.SealEnvelope(envelope)
		if err != nil {
			return finish(c, "/keys?error=bad_envelope#providers")
		}
		if cfg.RequiresBaseURL && baseOverride == "" {
			return finish(c, "/keys?error=base_url_required#providers")
		}
		if cfg.CredMode != "" {
			baseOverride = "" // Bedrock / Vertex derive the endpoint from the credential JSON
		}
		enc = sealed
		hintSrc = strings.TrimSpace(c.FormValue("hint"))
		if len(hintSrc) > 4 {
			hintSrc = hintSrc[len(hintSrc)-4:]
		}
		hintSrc = "…" + hintSrc
		return addProviderKey(c, userID, provider, enc, hintSrc, label, baseOverride, weight)
	}

	switch cfg.CredMode {
	case "aws":
		access := strings.TrimSpace(c.FormValue("aws_access_key_id"))
		secret := strings.TrimSpace(c.FormValue("aws_secret_access_key"))
		session := strings.TrimSpace(c.FormValue("aws_session_token"))
		region := strings.TrimSpace(c.FormValue("aws_region"))
		if region == "" {
			region = strings.TrimSpace(c.FormValue("base_url")) // allow region via base_url field
		}
		if access == "" || secret == "" {
			return finish(c, "/keys?error=empty_aws#providers")
		}
		plaintext, err = providers.MarshalAWSCreds(access, secret, session, region)
		if err != nil {
			return finish(c, "/keys?error=bad_creds#providers")
		}
		hintSrc = access
		baseOverride = "" // Bedrock derives endpoint from region inside the vault blob
	case "vertex":
		sa := strings.TrimSpace(c.FormValue("vertex_sa_json"))
		project := strings.TrimSpace(c.FormValue("vertex_project"))
		location := strings.TrimSpace(c.FormValue("vertex_location"))
		if sa == "" || project == "" {
			return finish(c, "/keys?error=empty_vertex#providers")
		}
		plaintext, err = providers.MarshalVertexCreds(sa, project, location)
		if err != nil {
			return finish(c, "/keys?error=bad_creds#providers")
		}
		hintSrc = project
		baseOverride = "" // Vertex derives endpoint from project/location
	default:
		if apiKey == "" {
			return finish(c, "/keys?error=empty_key#providers")
		}
		if cfg.RequiresBaseURL && baseOverride == "" {
			return finish(c, "/keys?error=base_url_required#providers")
		}
		plaintext = apiKey
		hintSrc = apiKey
	}

	enc, err = appcrypto.Encrypt(plaintext)
	if err != nil {
		return finish(c, "/keys?error=encrypt_failed#providers")
	}
	return addProviderKey(c, userID, provider, enc, appcrypto.Hint(hintSrc), label, baseOverride, weight)
}

// addProviderKey stores an already-encrypted credential as one more key for
// (user, provider). Several keys per provider share traffic by weight.
func addProviderKey(c *fiber.Ctx, userID uuid.UUID, provider, enc, hint, label, baseOverride string, weight int) error {
	if err := database.Get().Create(&models.ProviderKey{
		UserID:          userID,
		Provider:        provider,
		EncryptedKey:    enc,
		KeyHint:         hint,
		Label:           label,
		BaseURLOverride: baseOverride,
		Weight:          weight,
	}).Error; err != nil {
		return finish(c, "/keys?error=save_failed#providers")
	}
	return finish(c, "/keys#providers")
}

// UpdateProviderKeyHandler changes a key's label and weight, and clears its
// cooldown so it is tried again right away.
func UpdateProviderKeyHandler(c *fiber.Ctx) error {
	userID, err := auth.GetUserID(c)
	if err != nil || userID == uuid.Nil {
		return c.Redirect("/auth")
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return finish(c, "/keys#providers")
	}
	weight, ok := parseWeight(c.FormValue("weight"))
	if !ok {
		return finish(c, "/keys?error=bad_weight#providers")
	}
	res := database.Get().Model(&models.ProviderKey{}).Where("id = ? AND user_id = ?", id, userID).
		Updates(map[string]interface{}{
			"label":          strings.TrimSpace(c.FormValue("label")),
			"weight":         weight,
			"cooldown_until": nil,
			"last_error":     "",
		})
	if res.Error != nil {
		return finish(c, "/keys?error=save_failed#providers")
	}
	return finish(c, "/keys#providers")
}

// parseWeight accepts 1-100; blank means 1.
func parseWeight(v string) (int, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 1, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 100 {
		return 0, false
	}
	return n, true
}

func coolingLabel(until *time.Time, now time.Time) string {
	if until == nil || !until.After(now) {
		return ""
	}
	return until.UTC().Format("15:04") + " UTC"
}

// DeleteProviderKeyHandler removes a stored provider key.
func DeleteProviderKeyHandler(c *fiber.Ctx) error {
	userID, err := auth.GetUserID(c)
	if err != nil || userID == uuid.Nil {
		return c.Redirect("/auth")
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return finish(c, "/keys#providers")
	}
	database.Get().Unscoped().Where("id = ? AND user_id = ?", id, userID).Delete(&models.ProviderKey{})
	return finish(c, "/keys#providers")
}

// CreatePlatformKeyHandler mints an extra sk-… bearer token (shown once).
func CreatePlatformKeyHandler(c *fiber.Ctx) error {
	userID, err := auth.GetUserID(c)
	if err != nil || userID == uuid.Nil {
		return c.Redirect("/auth")
	}

	name := strings.TrimSpace(c.FormValue("name"))
	provider := strings.ToLower(strings.TrimSpace(c.FormValue("allowed_provider")))
	model := strings.TrimSpace(c.FormValue("allowed_model"))
	if provider == "" {
		model = "" // model scope requires provider
	}
	if provider != "" {
		if _, ok := providers.Get(provider); !ok {
			return finish(c, "/keys?error=unknown_provider#providers")
		}
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
	plain, err := MintWallet(userID, WalletSpec{
		Name:             name,
		AllowedProvider:  provider,
		AllowedModel:     model,
		DailyTokenLimit:  parseInt64Form(c.FormValue("daily_token_limit")),
		WeeklyTokenLimit: parseInt64Form(c.FormValue("weekly_token_limit")),
		DailyUSDMicros:   DollarsToMicros(c.FormValue("daily_usd_cap")),
		WeeklyUSDMicros:  DollarsToMicros(c.FormValue("weekly_usd_cap")),
		MonthlyUSDMicros: DollarsToMicros(c.FormValue("monthly_usd_cap")),
		SessionUSDMicros: DollarsToMicros(c.FormValue("session_usd_cap")),
		RPMLimit:         parseInt64Form(c.FormValue("rpm_limit")),
		TPMLimit:         parseInt64Form(c.FormValue("tpm_limit")),
		CacheTTLSeconds:  parseCacheTTL(c.FormValue("cache_ttl")),
		AutoCacheOff:     c.FormValue("auto_cache") == "off",
		ExpiresAt:        expires,
	})
	if err != nil {
		return finish(c, "/keys?error=keygen_failed#tokens")
	}
	return finish(c, "/keys?new_key="+plain+"#tokens")
}

// SetTenantCapHandler upserts a monthly USD cap for one tenant.
func SetTenantCapHandler(c *fiber.Ctx) error {
	userID, err := auth.GetUserID(c)
	if err != nil || userID == uuid.Nil {
		return c.Redirect("/auth")
	}
	tenant := strings.TrimSpace(c.FormValue("tenant"))
	if tenant == "" {
		return finish(c, "/keys?error=tenant_required#tenants")
	}
	if err := UpsertTenantCap(userID, tenant, DollarsToMicros(c.FormValue("monthly_usd_cap"))); err != nil {
		return finish(c, "/keys?error=tenant_failed#tenants")
	}
	return finish(c, "/keys#tenants")
}

// ReceiptPageHandler shows wallet / session / tenant spend vs list price.
func ReceiptPageHandler(c *fiber.Ctx) error {
	userID, err := auth.GetUserID(c)
	if err != nil || userID == uuid.Nil {
		return c.Redirect("/auth")
	}
	var keyID *uuid.UUID
	if raw := strings.TrimSpace(c.Query("key")); raw != "" {
		id, err := uuid.Parse(raw)
		if err == nil {
			keyID = &id
		}
	}
	receipt, err := LoadReceipt(userID, keyID, c.Query("session"), c.Query("tenant"))
	if err != nil {
		return c.Status(500).SendString("could not load receipt")
	}
	if strings.Contains(c.Get("Accept"), "application/json") {
		return c.JSON(receipt)
	}
	var user models.User
	_ = database.Get().First(&user, "id = ?", userID)
	return c.JSON(fiber.Map{
		"Title":      "Receipt",
		"User":       user,
		"Session":    strings.TrimSpace(c.Query("session")),
		"Tenant":     strings.TrimSpace(c.Query("tenant")),
		"Key":        strings.TrimSpace(c.Query("key")),
		"Wallet":     MicrosToDollars(receipt.WalletUSDMicros),
		"SessionUSD": MicrosToDollars(receipt.SessionUSDMicros),
		"TenantUSD":  MicrosToDollars(receipt.TenantUSDMicros),
		"List":       MicrosToDollars(receipt.ListUSDMicros),
		"Cached":     receipt.CachedTokens,
		"Reasoning":  receipt.ReasoningTokens,
		"Calls":      receipt.Calls,
	})
}

func parseInt64Form(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	var n int64
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int64(ch-'0')
		if n > 1_000_000_000_000 {
			return 0
		}
	}
	return n
}

// RotatePlatformKeyHandler revokes every bearer token. Nothing is minted in
// its place — tokens are only ever created when the user asks for one.
func RotatePlatformKeyHandler(c *fiber.Ctx) error {
	userID, err := auth.GetUserID(c)
	if err != nil || userID == uuid.Nil {
		return c.Redirect("/auth")
	}
	var ids []uuid.UUID
	database.Get().Model(&models.PlatformAPIKey{}).Where("user_id = ?", userID).Pluck("id", &ids)
	database.Get().Where("user_id = ?", userID).Delete(&models.PlatformAPIKey{})
	for _, id := range ids {
		_ = memory.DeleteForKey(id)
	}
	return finish(c, "/keys#tokens")
}

// DeletePlatformKeyHandler revokes a platform API key.
func DeletePlatformKeyHandler(c *fiber.Ctx) error {
	userID, err := auth.GetUserID(c)
	if err != nil || userID == uuid.Nil {
		return c.Redirect("/auth")
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return finish(c, "/keys#tokens")
	}
	if res := database.Get().Where("id = ? AND user_id = ?", id, userID).Delete(&models.PlatformAPIKey{}); res.RowsAffected > 0 {
		_ = memory.DeleteForKey(id) // unreadable without the token anyway; don't keep the ciphertext
	}
	return finish(c, "/keys#tokens")
}

// ResolvePlatformKey returns the PlatformAPIKey for Authorization: Bearer sk-...
func ResolvePlatformKey(c *fiber.Ctx) (models.PlatformAPIKey, error) {
	authHdr := c.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(authHdr), "bearer ") {
		return models.PlatformAPIKey{}, fiber.ErrUnauthorized
	}
	raw := strings.TrimSpace(authHdr[7:])
	if raw == "" {
		return models.PlatformAPIKey{}, fiber.ErrUnauthorized
	}

	var pk models.PlatformAPIKey
	if err := database.Get().Where("key_hash = ?", appcrypto.HashAPIKey(raw)).First(&pk).Error; err != nil {
		return models.PlatformAPIKey{}, fiber.ErrUnauthorized
	}
	switch tokenStatus(pk, time.Now()) {
	case "paused":
		return models.PlatformAPIKey{}, ErrKeyPaused
	case "expired":
		return models.PlatformAPIKey{}, ErrKeyExpired
	}

	// "Last used" only needs minute precision; skipping the write on most
	// calls keeps a busy token from updating the same row on every request.
	now := time.Now()
	if pk.LastUsedAt == nil || now.Sub(*pk.LastUsedAt) >= time.Minute {
		database.Get().Model(&models.PlatformAPIKey{}).
			Where("id = ? AND (last_used_at IS NULL OR last_used_at < ?)", pk.ID, now.Add(-time.Minute)).
			Update("last_used_at", now)
	}
	return pk, nil
}

// ResolveUserFromBearer returns the user for Authorization: Bearer sk-...
func ResolveUserFromBearer(c *fiber.Ctx) (uuid.UUID, error) {
	pk, err := ResolvePlatformKey(c)
	if err != nil {
		return uuid.Nil, err
	}
	return pk.UserID, nil
}

func sumKeyTokens(keyID uuid.UUID) (dayUsed, weekUsed int64) {
	db := database.Get()
	now := time.Now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	weekStart := dayStart.AddDate(0, 0, -(weekday - 1))
	_ = db.Model(&models.UsageEvent{}).
		Where("platform_api_key_id = ? AND created_at >= ? AND ((status_code >= 200 AND status_code < 300) OR status_code = 499) AND cache_hit IS NOT TRUE", keyID, dayStart).
		Select("COALESCE(SUM(total_tokens),0)").Scan(&dayUsed)
	_ = db.Model(&models.UsageEvent{}).
		Where("platform_api_key_id = ? AND created_at >= ? AND ((status_code >= 200 AND status_code < 300) OR status_code = 499) AND cache_hit IS NOT TRUE", keyID, weekStart).
		Select("COALESCE(SUM(total_tokens),0)").Scan(&weekUsed)
	return
}

func sumKeyUSD(keyID uuid.UUID) (dayUsed, weekUsed int64) {
	db := database.Get()
	now := time.Now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	weekStart := dayStart.AddDate(0, 0, -(weekday - 1))
	_ = db.Model(&models.UsageEvent{}).
		Where("platform_api_key_id = ? AND created_at >= ? AND ((status_code >= 200 AND status_code < 300) OR status_code = 499)", keyID, dayStart).
		Select("COALESCE(SUM(cost_usd_micros),0)").Scan(&dayUsed)
	_ = db.Model(&models.UsageEvent{}).
		Where("platform_api_key_id = ? AND created_at >= ? AND ((status_code >= 200 AND status_code < 300) OR status_code = 499)", keyID, weekStart).
		Select("COALESCE(SUM(cost_usd_micros),0)").Scan(&weekUsed)
	return
}

// tokenSummary renders spend + caps for one token without "$0.0112/$0"-style
// noise: usage is always shown, a cap only when it is set.
func tokenSummary(k models.PlatformAPIKey, dayTok, weekTok, dayUSD, weekUSD int64) string {
	var parts []string
	used := func(label string, usedMicros, capMicros int64) {
		if capMicros > 0 {
			parts = append(parts, fmt.Sprintf("%s $%s of $%s", label, MicrosToDollars(usedMicros), MicrosToDollars(capMicros)))
		} else if usedMicros > 0 {
			parts = append(parts, fmt.Sprintf("%s $%s", label, MicrosToDollars(usedMicros)))
		}
	}
	used("Day", dayUSD, k.DailyUSDMicros)
	used("Week", weekUSD, k.WeeklyUSDMicros)
	if k.MonthlyUSDMicros > 0 {
		parts = append(parts, "Month cap $"+MicrosToDollars(k.MonthlyUSDMicros))
	}
	if k.SessionUSDMicros > 0 {
		parts = append(parts, "Session cap $"+MicrosToDollars(k.SessionUSDMicros))
	}
	if k.DailyTokenLimit > 0 {
		parts = append(parts, fmt.Sprintf("Day %d of %d tokens", dayTok, k.DailyTokenLimit))
	}
	if k.WeeklyTokenLimit > 0 {
		parts = append(parts, fmt.Sprintf("Week %d of %d tokens", weekTok, k.WeeklyTokenLimit))
	}
	if k.RPMLimit > 0 {
		parts = append(parts, fmt.Sprintf("%d rpm", k.RPMLimit))
	}
	if k.TPMLimit > 0 {
		parts = append(parts, fmt.Sprintf("%d tpm", k.TPMLimit))
	}
	if k.CacheTTLSeconds > 0 {
		parts = append(parts, fmt.Sprintf("cache %ds", k.CacheTTLSeconds))
	}
	if len(parts) == 0 {
		return "No caps · no spend yet"
	}
	hasCap := k.DailyUSDMicros > 0 || k.WeeklyUSDMicros > 0 || k.MonthlyUSDMicros > 0 || k.SessionUSDMicros > 0 ||
		k.DailyTokenLimit > 0 || k.WeeklyTokenLimit > 0 || k.RPMLimit > 0 || k.TPMLimit > 0
	if !hasCap {
		parts = append(parts, "no caps")
	}
	return strings.Join(parts, " · ")
}

// keysFlash maps a /keys error code to its message; unknown codes pass through.
func keysFlash(code string) string {
	switch code {
	case "":
		return ""
	case "unknown_provider":
		return "Unknown provider"
	case "empty_key":
		return "API key is required"
	case "empty_aws":
		return "AWS access key and secret are required for Bedrock"
	case "empty_vertex":
		return "Service account JSON and GCP project are required for Vertex"
	case "bad_creds":
		return "Could not build provider credentials — check the form fields"
	case "base_url_required":
		return "This provider needs an API base URL (Azure resource, Cloudflare account AI URL, Databricks workspace, or custom endpoint)"
	case "encrypt_failed":
		return "Could not encrypt key — check KEY_ENCRYPTION_SECRET / COOKIE_SECRET"
	case "bad_envelope":
		return "The sealed key could not be accepted — reload the page and try again"
	case "save_failed":
		return "Could not save"
	case "bad_weight":
		return "Weight must be a whole number from 1 to 100"
	case "keygen_failed":
		return "Could not mint bearer token"
	case "tenant_required":
		return "Tenant id is required"
	case "tenant_failed":
		return "Could not save tenant cap"
	case "bad_expiry":
		return "Expiry must be a future date (YYYY-MM-DD)"
	case "token_not_found":
		return "Token not found"
	}
	return code
}

// parseCacheTTL reads a token's default response-cache TTL in seconds,
// clamped to 30 days; blank or invalid means off.
func parseCacheTTL(v string) int64 {
	n := parseInt64Form(v)
	if n <= 0 {
		return 0
	}
	return min(n, 30*24*3600)
}
