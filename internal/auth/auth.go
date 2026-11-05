package auth

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/vrannang1/spendmux/internal/core"
	"github.com/vrannang1/spendmux/internal/database"
	"github.com/vrannang1/spendmux/internal/utils"
	"github.com/vrannang1/spendmux/models"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	sessionCookie = "spendmux_session"
	sessionTTL    = 30 * 24 * 60 * 60 // 30 days
)

// ErrIdentityConflict is returned when linking an identity already owned elsewhere.
var ErrIdentityConflict = errors.New("identity already linked to another account")

// ── Auth seam (over the session cookie + vault) ───────────────

// AuthStore saves the UUID into the session store and sets the cookie.
func AuthStore(c *fiber.Ctx, userID uuid.UUID) error {
	orgID := uuid.Nil
	if user, err := core.LoadUser(userID); err == nil && user.DefaultOrgID != nil {
		orgID = *user.DefaultOrgID
	}
	StartSession(c, userID, orgID)
	return nil
}

// GetUserID reads the session and returns the authenticated user UUID.
// Empty / missing session returns (uuid.Nil, nil).
func GetUserID(c *fiber.Ctx) (uuid.UUID, error) {
	sessionID := strings.TrimSpace(c.Cookies(sessionCookie))
	if sessionID == "" {
		return uuid.Nil, nil
	}
	userIDStr, ok := core.GetSession(sessionID)
	if !ok || userIDStr == "" {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(userIDStr)
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// AuthLogout clears the session cookie and vault entry.
func AuthLogout(c *fiber.Ctx) error {
	sessionID := strings.TrimSpace(c.Cookies(sessionCookie))
	if sessionID != "" {
		core.DeleteSession(sessionID)
	}
	clearSessionCookie(c)
	return nil
}

// ── Login page ────────────────────────────────────────────────

func LoginPage(c *fiber.Ctx) error {
	return core.ServeApp(c, "login")
}

// ── OAuth ─────────────────────────────────────────────────────

func RedirectToProvider(c *fiber.Ctx) error {
	provider := c.Params("provider")
	p := core.GetProvider(provider)
	if p == nil {
		return redirectWithError(c, "Unknown provider")
	}
	clientID := p.ClientID()
	redirectURI := p.RedirectURI()
	if clientID == "" || redirectURI == "" {
		return redirectWithError(c, "OAuth not configured")
	}
	state := utils.GenerateID()
	setOAuthBrowserCookie(c, state)
	core.SetOAuthState(state, provider, core.OAuthStateExtra{})
	return c.Redirect(authorizeURL(p, clientID, redirectURI, state), fiber.StatusFound)
}

func RedirectToProviderLink(c *fiber.Ctx) error {
	uid, err := GetUserID(c)
	if err != nil || uid == uuid.Nil {
		return c.Redirect("/login", fiber.StatusFound)
	}
	provider := c.Params("provider")
	p := core.GetProvider(provider)
	if p == nil {
		return c.Redirect("/account?error=unknown_provider", fiber.StatusFound)
	}
	clientID := p.ClientID()
	redirectURI := p.RedirectURI()
	if clientID == "" || redirectURI == "" {
		return c.Redirect("/account?error=oauth_not_configured", fiber.StatusFound)
	}
	state := utils.GenerateID()
	setOAuthBrowserCookie(c, state)
	core.SetOAuthState(state, provider, core.OAuthStateExtra{LinkUserID: uid.String()})
	if provider == "apple" && secureSessionCookie() {
		c.Cookie(&fiber.Cookie{Name: "spendmux_oauth_link", Value: c.Cookies(sessionCookie), Path: "/auth/apple/callback", MaxAge: 600, HTTPOnly: true, Secure: true, SameSite: "None"})
	}
	return c.Redirect(authorizeURL(p, clientID, redirectURI, state), fiber.StatusFound)
}

func authorizeURL(p *core.OAuthProvider, clientID, redirectURI, state string) string {
	params := url.Values{
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"response_type": {"code"},
		"scope":         {p.Scope},
		"state":         {state},
	}
	for k, v := range p.ExtraAuthorizeParams {
		params.Set(k, v)
	}
	return p.AuthorizeURL() + "?" + params.Encode()
}

func OAuthCallback(c *fiber.Ctx) error {
	provider := c.Params("provider")
	p := core.GetProvider(provider)
	if p == nil {
		return redirectWithError(c, "Unknown provider")
	}

	get := func(k string) string {
		if c.Method() == fiber.MethodPost {
			return c.FormValue(k)
		}
		return c.Query(k)
	}
	source := url.Values{}
	for _, k := range []string{"code", "state", "error", "user"} {
		if v := get(k); v != "" {
			source.Set(k, v)
		}
	}

	if source.Get("error") != "" {
		return redirectWithError(c, "Authentication cancelled")
	}
	code := source.Get("code")
	state := source.Get("state")
	if code == "" || state == "" {
		return redirectWithError(c, "Invalid callback parameters")
	}
	browserState := c.Cookies("spendmux_oauth_state")
	if browserState == "" || subtle.ConstantTimeCompare([]byte(browserState), []byte(state)) != 1 {
		return redirectWithError(c, "OAuth browser state mismatch")
	}
	c.Cookie(&fiber.Cookie{Name: "spendmux_oauth_state", Value: "", Path: "/", MaxAge: -1, HTTPOnly: true, Secure: secureSessionCookie(), SameSite: oauthCookieSameSite()})
	extra, ok := core.TakeOAuthState(state, provider)
	if !ok {
		return redirectWithError(c, "Invalid state parameter")
	}

	if extra.LinkUserID != "" {
		uid, err := GetUserID(c)
		if uid == uuid.Nil && provider == "apple" && c.Method() == fiber.MethodPost {
			if linked, ok := core.GetSession(c.Cookies("spendmux_oauth_link")); ok {
				uid, err = uuid.Parse(linked)
			}
		}
		c.Cookie(&fiber.Cookie{Name: "spendmux_oauth_link", Value: "", Path: "/auth/apple/callback", MaxAge: -1, HTTPOnly: true, Secure: secureSessionCookie(), SameSite: oauthCookieSameSite()})
		if err != nil || uid.String() != extra.LinkUserID {
			return redirectWithError(c, "Sign in to the initiating account before linking")
		}
	}
	tokenResp, err := core.ExchangeOAuthCode(p, code)
	if err != nil {
		return redirectWithError(c, "Failed to exchange code")
	}
	info, err := p.FetchUserInfo(tokenResp, source)
	if err != nil || info == nil || info.OAuthID == "" {
		return redirectWithError(c, "Failed to get user info")
	}

	userID, orgID, err := FindOrCreateUser(*info, provider, extra.LinkUserID)
	if errors.Is(err, ErrIdentityConflict) {
		return c.Redirect("/account?error=identity_already_linked", fiber.StatusFound)
	}
	if err != nil {
		return redirectWithError(c, "Failed to create user")
	}

	_ = AuthStore(c, userID)
	if orgID != uuid.Nil {
		SetCurrentOrgID(c, orgID)
	}
	return c.Redirect("/account", fiber.StatusFound)
}

func Logout(c *fiber.Ctx) error {
	_ = AuthLogout(c)
	return core.JSON(c, http.StatusOK, fiber.Map{"success": true})
}

func CurrentUser(c *fiber.Ctx) error {
	userID, err := GetUserID(c)
	if err != nil || userID == uuid.Nil {
		return core.JSON(c, http.StatusUnauthorized, fiber.Map{"error": "Not authenticated"})
	}
	user, err := core.LoadUser(userID)
	if err != nil {
		return core.JSON(c, http.StatusNotFound, fiber.Map{"error": "User not found"})
	}

	provider := user.Provider
	if provider == "" {
		provider = "oauth"
	}
	var defaultOrg any
	if user.DefaultOrgID != nil {
		defaultOrg = user.DefaultOrgID.String()
	}

	return core.JSON(c, http.StatusOK, fiber.Map{
		"id":                      user.ID.String(),
		"email_masked":            models.MaskEmail(user.Email),
		"full_name":               user.Name,
		"name":                    user.Name,
		"provider":                provider,
		"role":                    user.Role,
		"is_admin":                user.IsAdmin(),
		"keep_request_history":    user.KeepRequestHistory,
		"avatar_url":              user.AvatarURL,
		"credits":                 user.Credits,
		"created_at":              user.CreatedAt.Unix(),
		"onboarding_completed_at": unixPtr(user.OnboardingCompletedAt),
		"default_org_id":          defaultOrg,
	})
}

func ListIdentities(c *fiber.Ctx) error {
	uid, err := GetUserID(c)
	if err != nil || uid == uuid.Nil {
		return core.JSON(c, http.StatusUnauthorized, fiber.Map{"error": "Not authenticated"})
	}
	var identities []models.UserIdentity
	database.Get().Where("user_id = ?", uid).Order("created_at asc").Find(&identities)

	out := make([]map[string]any, 0, len(identities))
	for _, ident := range identities {
		out = append(out, map[string]any{
			"provider":     ident.Provider,
			"connected_at": ident.CreatedAt.Unix(),
		})
	}
	return core.JSON(c, http.StatusOK, fiber.Map{
		"identities":   out,
		"has_password": false,
	})
}

func UnlinkIdentity(c *fiber.Ctx) error {
	uid, err := GetUserID(c)
	if err != nil || uid == uuid.Nil {
		return core.JSON(c, http.StatusUnauthorized, fiber.Map{"error": "Not authenticated"})
	}
	provider := c.Params("provider")
	db := database.Get()

	var identities []models.UserIdentity
	db.Where("user_id = ?", uid).Find(&identities)
	connected := false
	for _, ident := range identities {
		if ident.Provider == provider {
			connected = true
			break
		}
	}
	if !connected {
		return core.JSON(c, http.StatusNotFound, fiber.Map{"error": "Not connected"})
	}
	if len(identities) <= 1 {
		return core.JSON(c, http.StatusBadRequest, fiber.Map{"error": "Cannot remove your only sign-in method"})
	}
	if err := db.Where("user_id = ? AND provider = ?", uid, provider).Delete(&models.UserIdentity{}).Error; err != nil {
		return core.JSON(c, http.StatusInternalServerError, fiber.Map{"error": "Failed to unlink"})
	}
	return core.JSON(c, http.StatusOK, fiber.Map{"success": true})
}

func DeleteAccount(c *fiber.Ctx) error {
	userID, err := GetUserID(c)
	if err != nil || userID == uuid.Nil {
		return core.JSON(c, http.StatusUnauthorized, fiber.Map{"error": "Not authenticated"})
	}
	// The caller must type their own email address; compared server-side so the
	// page never has to reveal the full address.
	var body struct {
		Confirmation string `json:"confirmation"`
	}
	_ = c.BodyParser(&body)
	user, err := core.LoadUser(userID)
	if err != nil {
		return core.JSON(c, http.StatusNotFound, fiber.Map{"error": "User not found"})
	}
	typed := strings.ToLower(strings.TrimSpace(body.Confirmation))
	if typed == "" || typed != strings.ToLower(strings.TrimSpace(user.Email)) {
		return core.JSON(c, http.StatusBadRequest, fiber.Map{"error": "confirmation_mismatch", "message": "Type your account email exactly to confirm deletion"})
	}
	err = database.Get().Transaction(func(tx *gorm.DB) error {
		return deleteAccountData(tx, userID)
	})
	if err != nil {
		if errors.Is(err, errSharedOrganization) {
			return core.JSON(c, http.StatusConflict, fiber.Map{"error": "deletion_blocked", "message": err.Error()})
		}
		return core.JSON(c, http.StatusServiceUnavailable, fiber.Map{"error": "delete_failed", "message": "Deletion could not be safely completed. Your account has been retained; please retry."})
	}
	_ = AuthLogout(c)
	return core.JSON(c, http.StatusOK, fiber.Map{"success": true})
}

var errSharedOrganization = errors.New("Remove members from organizations you created before deleting your account; shared organization data cannot be deleted with your account.")

func deleteAccountData(tx *gorm.DB, userID uuid.UUID) error {
	var user models.User
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, "id = ?", userID).Error; err != nil {
		return err
	}
	var orgIDs []uuid.UUID
	if err := tx.Model(&models.Org{}).Where("created_by = ?", userID).Pluck("id", &orgIDs).Error; err != nil {
		return err
	}
	if len(orgIDs) > 0 {
		var others int64
		if err := tx.Model(&models.OrgMember{}).Where("org_id IN ? AND user_id <> ?", orgIDs, userID).Count(&others).Error; err != nil {
			return err
		}
		if others > 0 {
			return errSharedOrganization
		}
	}
	for _, model := range []interface{}{&models.TokenReservation{}, &models.PlatformKeyRateLimitBucket{}, &models.LoopFingerprintBucket{}} {
		if err := tx.Unscoped().Where("platform_api_key_id IN (?)", tx.Unscoped().Model(&models.PlatformAPIKey{}).Select("id").Where("user_id = ?", userID)).Delete(model).Error; err != nil {
			return err
		}
	}
	for _, model := range []interface{}{
		&models.ProviderKey{}, &models.PlatformAPIKey{}, &models.UsageEvent{},
		&models.TenantCap{}, &models.FallbackChain{}, &models.Message{}, &models.Conversation{},
		&models.Session{}, &models.UserIdentity{}, &models.MemoryThread{},
		&models.ResponseCache{},
	} {
		if err := tx.Unscoped().Where("user_id = ?", userID).Delete(model).Error; err != nil {
			return err
		}
	}
	if err := tx.Where("link_user_id = ?", userID.String()).Delete(&models.OAuthState{}).Error; err != nil {
		return err
	}
	invites := tx.Unscoped().Where("invited_by = ?", userID)
	if user.EmailHash != "" {
		invites = invites.Or("email_hash = ?", user.EmailHash)
	}
	if err := invites.Delete(&models.OrgInvite{}).Error; err != nil {
		return err
	}
	if err := tx.Unscoped().Where("user_id = ?", userID).Delete(&models.OrgMember{}).Error; err != nil {
		return err
	}
	if len(orgIDs) > 0 {
		for _, model := range []interface{}{&models.OrgInvite{}, &models.OrgMember{}} {
			if err := tx.Unscoped().Where("org_id IN ?", orgIDs).Delete(model).Error; err != nil {
				return err
			}
		}
		if err := tx.Unscoped().Where("id IN ?", orgIDs).Delete(&models.Org{}).Error; err != nil {
			return err
		}
	}
	return tx.Unscoped().Delete(&models.User{}, "id = ?", userID).Error
}

// UpdateSettings saves account preferences. Body: {"keep_request_history": bool}
func UpdateSettings(c *fiber.Ctx) error {
	userID, err := GetUserID(c)
	if err != nil || userID == uuid.Nil {
		return core.JSON(c, http.StatusUnauthorized, fiber.Map{"error": "Not authenticated"})
	}
	var body struct {
		KeepRequestHistory *bool `json:"keep_request_history"`
	}
	if err := c.BodyParser(&body); err != nil || body.KeepRequestHistory == nil {
		return core.JSON(c, http.StatusBadRequest, fiber.Map{"error": "keep_request_history (bool) required"})
	}
	if err := database.Get().Model(&models.User{}).Where("id = ?", userID).Update("keep_request_history", *body.KeepRequestHistory).Error; err != nil {
		return core.JSON(c, http.StatusInternalServerError, fiber.Map{"error": "save_failed"})
	}
	return core.JSON(c, http.StatusOK, fiber.Map{"ok": true, "keep_request_history": *body.KeepRequestHistory})
}

// ── Session helpers ───────────────────────────────────────────

func IsAuthenticated(c *fiber.Ctx) bool {
	uid, err := GetUserID(c)
	return err == nil && uid != uuid.Nil
}

func GetCurrentOrgID(c *fiber.Ctx) uuid.UUID {
	sessionID := strings.TrimSpace(c.Cookies(sessionCookie))
	if sessionID == "" {
		return uuid.Nil
	}
	orgIDStr, _ := core.GetSessionOrgID(sessionID)
	id, err := uuid.Parse(orgIDStr)
	if err != nil {
		return uuid.Nil
	}
	return id
}

func SetCurrentOrgID(c *fiber.Ctx, orgID uuid.UUID) {
	sessionID := strings.TrimSpace(c.Cookies(sessionCookie))
	if sessionID != "" && orgID != uuid.Nil {
		core.SetSessionOrgID(sessionID, orgID.String())
	}
}

func secureSessionCookie() bool {
	if os.Getenv("APP_ENV") == "production" {
		return true
	}
	base := os.Getenv("BASE_URL")
	return strings.HasPrefix(strings.ToLower(base), "https://")
}

func StartSession(c *fiber.Ctx, userID, orgID uuid.UUID) {
	sessionID := utils.GenerateID()
	orgStr := ""
	if orgID != uuid.Nil {
		orgStr = orgID.String()
	}
	core.SetSession(sessionID, userID.String(), orgStr, sessionTTL)
	c.Cookie(&fiber.Cookie{
		Name:     sessionCookie,
		Value:    sessionID,
		Path:     "/",
		MaxAge:   sessionTTL,
		HTTPOnly: true,
		SameSite: "Lax",
		Secure:   secureSessionCookie(),
	})
}

func clearSessionCookie(c *fiber.Ctx) {
	c.Cookie(&fiber.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HTTPOnly: true,
		Secure:   secureSessionCookie(),
		SameSite: "Lax",
	})
}

func redirectWithError(c *fiber.Ctx, msg string) error {
	return c.Redirect("/login?error="+url.QueryEscape(msg), fiber.StatusFound)
}

func unixPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}

// ── User / identity resolution ────────────────────────────────

func FindOrCreateUser(info core.OAuthUserInfo, provider, linkToUserID string) (uuid.UUID, uuid.UUID, error) {
	db := database.Get()
	// Admin comes from ADMIN_EMAILS, so it is granted only on an email the
	// provider verified.
	role := "user"
	if info.EmailVerified {
		role = core.RoleForEmail(info.Email)
	}

	var identity models.UserIdentity
	err := db.Where("provider = ? AND provider_user_id = ?", provider, info.OAuthID).First(&identity).Error
	if err == nil {
		if linkToUserID != "" && linkToUserID != identity.UserID.String() {
			return uuid.Nil, uuid.Nil, ErrIdentityConflict
		}
		// Struct updates run the email serializer (map updates would bypass it)
		// and skip zero values, so role is promote-only and blanks never overwrite.
		updates := models.User{Name: info.Name}
		if role == "admin" {
			updates.Role = role
		}
		if info.Email != "" {
			updates.Email = strings.ToLower(strings.TrimSpace(info.Email))
			if info.EmailVerified {
				updates.EmailHash = utils.HashEmail(info.Email)
			}
		}
		_ = db.Model(&models.User{}).Where("id = ?", identity.UserID).Updates(updates).Error
		return identity.UserID, defaultOrgID(identity.UserID), nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return uuid.Nil, uuid.Nil, err
	}

	if linkToUserID != "" {
		uid, err := uuid.Parse(linkToUserID)
		if err != nil {
			return uuid.Nil, uuid.Nil, err
		}
		ident := models.UserIdentity{
			UserID:         uid,
			Provider:       provider,
			ProviderUserID: info.OAuthID,
		}
		if err := db.Where("provider = ? AND provider_user_id = ?", provider, info.OAuthID).
			FirstOrCreate(&ident).Error; err != nil {
			return uuid.Nil, uuid.Nil, err
		}
		return uid, defaultOrgID(uid), nil
	}

	// Accounts created before user_identities existed carry the identity on
	// users.provider / users.provider_id (and may have no email_hash yet).
	// Match those first so a migrated user never collides with itself.
	{
		var legacy models.User
		err := db.Where("provider = ? AND provider_id = ?", provider, info.OAuthID).First(&legacy).Error
		if err == nil {
			ident := models.UserIdentity{UserID: legacy.ID, Provider: provider, ProviderUserID: info.OAuthID}
			_ = db.Where("provider = ? AND provider_user_id = ?", provider, info.OAuthID).FirstOrCreate(&ident).Error
			// Struct updates run the email serializer (map updates would bypass it)
			// and skip zero values, so role is promote-only and blanks never overwrite.
			updates := models.User{Name: info.Name}
			if role == "admin" {
				updates.Role = role
			}
			if info.Email != "" {
				updates.Email = strings.ToLower(strings.TrimSpace(info.Email))
				if info.EmailVerified {
					updates.EmailHash = utils.HashEmail(info.Email)
				}
			}
			_ = db.Model(&legacy).Updates(updates).Error
			return legacy.ID, defaultOrgID(legacy.ID), nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return uuid.Nil, uuid.Nil, err
		}
	}

	if info.Email != "" && info.EmailVerified {
		var existing models.User
		err := db.Where("email_hash IN ?", []string{utils.HashEmail(info.Email), utils.LegacyHashEmail(info.Email)}).First(&existing).Error
		if err == nil {
			ident := models.UserIdentity{
				UserID:         existing.ID,
				Provider:       provider,
				ProviderUserID: info.OAuthID,
			}
			_ = db.Where("provider = ? AND provider_user_id = ?", provider, info.OAuthID).
				FirstOrCreate(&ident).Error
			_ = db.Model(&existing).Updates(models.User{Email: strings.ToLower(strings.TrimSpace(info.Email)), Name: info.Name, EmailHash: utils.HashEmail(info.Email)}).Error
			orgID := uuid.Nil
			if existing.DefaultOrgID != nil {
				orgID = *existing.DefaultOrgID
			}
			return existing.ID, orgID, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return uuid.Nil, uuid.Nil, err
		}
	}

	fallbackEmail := info.Email
	if fallbackEmail == "" {
		fallbackEmail = fmt.Sprintf("%s_%s@placeholder.local", provider, info.OAuthID)
	}
	emailNorm := strings.ToLower(strings.TrimSpace(fallbackEmail))
	// email_hash is how a later sign-in finds this account by email, so only
	// an address the provider verified may claim it. Otherwise someone could
	// create an account under another person's email and have that person's
	// verified login merged into it.
	hashSource := fallbackEmail
	if !info.EmailVerified {
		hashSource = fmt.Sprintf("unverified:%s:%s", provider, info.OAuthID)
	}
	user := models.User{
		Email:      emailNorm,
		EmailHash:  utils.HashEmail(hashSource),
		Name:       info.Name,
		Provider:   provider,
		ProviderID: info.OAuthID,
		Role:       role,
		Username:   fmt.Sprintf("%s_%s", provider, info.OAuthID),
	}
	if err := db.Create(&user).Error; err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	ident := models.UserIdentity{
		UserID:         user.ID,
		Provider:       provider,
		ProviderUserID: info.OAuthID,
	}
	if err := db.Create(&ident).Error; err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	displayName := info.Name
	if displayName == "" {
		displayName = strings.Split(emailNorm, "@")[0]
	}
	orgID, err := core.CreatePersonalOrg(user.ID, displayName)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return user.ID, orgID, nil
}

// defaultOrgID returns the user's default org, creating a personal org for
// accounts that predate organisations (migrated from the pre-v2 shell).
func defaultOrgID(userID uuid.UUID) uuid.UUID {
	user, err := core.LoadUser(userID)
	if err != nil {
		return uuid.Nil
	}
	if user.DefaultOrgID != nil {
		return *user.DefaultOrgID
	}
	name := user.Name
	if name == "" {
		name = strings.Split(user.Email, "@")[0]
	}
	orgID, err := core.CreatePersonalOrg(user.ID, name)
	if err != nil {
		return uuid.Nil
	}
	return orgID
}

func oauthCookieSameSite() string {
	if secureSessionCookie() {
		return "None"
	} // Apple uses a cross-site form POST.
	return "Lax"
}

// setOAuthBrowserCookie binds the OAuth state to this browser. Path "/" so it
// also reaches the legacy /oauth/<provider>/callback redirect URIs that
// existing provider consoles (and *_CALLBACK_URL overrides) still use.
func setOAuthBrowserCookie(c *fiber.Ctx, state string) {
	c.Cookie(&fiber.Cookie{Name: "spendmux_oauth_state", Value: state, Path: "/", MaxAge: 600, HTTPOnly: true, Secure: secureSessionCookie(), SameSite: oauthCookieSameSite()})
}
