package core

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm/clause"

	"github.com/peragolabs/spendmux/internal/database"
	"github.com/peragolabs/spendmux/internal/supervision"
	"github.com/peragolabs/spendmux/models"
)

// ── Session store (GORM-backed) ───────────────────────────────

// SetSession stores a session with the active org (workspace) scope. orgId
// may be empty when the user has no default workspace yet.
func SetSession(id, userID, orgID string, ttlSeconds int64) {
	sid, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return
	}
	uid, err := uuid.Parse(strings.TrimSpace(userID))
	if err != nil {
		return
	}
	var orgPtr *uuid.UUID
	if strings.TrimSpace(orgID) != "" {
		if oid, err := uuid.Parse(orgID); err == nil {
			orgPtr = &oid
		}
	}
	expires := time.Now().UTC().Add(time.Duration(ttlSeconds) * time.Second)
	db := database.Get()
	var existing models.Session
	if err := db.Where("id = ?", sid).First(&existing).Error; err == nil {
		existing.UserID = uid
		existing.OrgID = orgPtr
		existing.ExpiresAt = expires
		_ = db.Save(&existing).Error
		return
	}
	_ = db.Create(&models.Session{
		Base:      models.Base{ID: sid},
		UserID:    uid,
		OrgID:     orgPtr,
		ExpiresAt: expires,
	}).Error
}

func GetSession(id string) (string, bool) {
	sid, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return "", false
	}
	var s models.Session
	if err := database.Get().Where("id = ? AND expires_at > ?", sid, time.Now().UTC()).First(&s).Error; err != nil {
		return "", false
	}
	return s.UserID.String(), true
}

// GetSessionOrgID returns the org (workspace) currently selected for the session.
func GetSessionOrgID(id string) (string, bool) {
	sid, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return "", false
	}
	var s models.Session
	if err := database.Get().Where("id = ? AND expires_at > ?", sid, time.Now().UTC()).First(&s).Error; err != nil {
		return "", false
	}
	if s.OrgID == nil || *s.OrgID == uuid.Nil {
		return "", false
	}
	return s.OrgID.String(), true
}

// SetSessionOrgID switches the active workspace for an existing session.
func SetSessionOrgID(id, orgID string) {
	sid, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return
	}
	var orgPtr *uuid.UUID
	if strings.TrimSpace(orgID) != "" {
		if oid, err := uuid.Parse(orgID); err == nil {
			orgPtr = &oid
		}
	}
	_ = database.Get().Model(&models.Session{}).Where("id = ?", sid).Update("org_id", orgPtr).Error
}

func DeleteSession(id string) {
	sid, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return
	}
	_ = database.Get().Unscoped().Delete(&models.Session{}, "id = ?", sid).Error
}

// CleanupExpiredSessions deletes expired session rows.
func CleanupExpiredSessions() {
	_ = database.Get().Unscoped().Where("expires_at <= ?", time.Now().UTC()).Delete(&models.Session{}).Error
}

// ── OAuth state store ─────────────────────────────────────────

// OAuthStateExtra carries flow-specific data alongside an OAuth state token.
// LinkUserID is set for the "Connect <provider>" flow (already-logged-in user
// linking a new identity).
type OAuthStateExtra struct {
	LinkUserID string
}

const oauthStateTTL = 300

// OAuth states live in the database (hashed, single-use, five minutes) so a
// login survives a deploy and a callback can land on any replica, including
// Apple's cross-site form POST.
func oauthStateID(state string) string {
	sum := sha256.Sum256([]byte("oauth-state:" + state))
	return hex.EncodeToString(sum[:])
}

func SetOAuthState(state, provider string, extra OAuthStateExtra) {
	row := models.OAuthState{
		ID: oauthStateID(state), Provider: provider,
		LinkUserID: extra.LinkUserID, CreatedAt: time.Now().UTC(),
	}
	if err := database.Get().Create(&row).Error; err != nil {
		slog.Error("oauth_state_save_failed", "error", err)
	}
}

// TakeOAuthState consumes the state (single-use) and returns its extra data if
// it is valid for the expected provider and not expired.
func TakeOAuthState(state, provider string) (OAuthStateExtra, bool) {
	if state == "" {
		return OAuthStateExtra{}, false
	}
	var row models.OAuthState
	res := database.Get().Clauses(clause.Returning{}).Where("id = ?", oauthStateID(state)).Delete(&row)
	if res.Error != nil || res.RowsAffected != 1 {
		return OAuthStateExtra{}, false
	}
	if row.Provider != provider || time.Since(row.CreatedAt) >= oauthStateTTL*time.Second {
		return OAuthStateExtra{}, false
	}
	return OAuthStateExtra{LinkUserID: row.LinkUserID}, true
}

// StartCleanup runs a goroutine to purge expired OAuth states and rate limits.
// Expired session rows are cleaned hourly via StartSessionCleanup in jobs.go.
func StartCleanup() {
	supervision.Every("oauth-state-cleanup", 60*time.Second, false, func() {
		now := time.Now().Unix()
		_ = database.Get().Where("created_at < ?", time.Now().UTC().Add(-oauthStateTTL*time.Second)).Delete(&models.OAuthState{}).Error

		rateMu.Lock()
		for ip, stamps := range rateLimits {
			fresh := stamps[:0]
			for _, t := range stamps {
				if now-t < rateLimitWindow {
					fresh = append(fresh, t)
				}
			}
			if len(fresh) == 0 {
				delete(rateLimits, ip)
			} else {
				rateLimits[ip] = fresh
			}
		}
		rateMu.Unlock()
	})
}

// ── Rate limiting (contact form anti-spam) ────────────────────

const (
	rateLimitWindow      int64 = 900 // 15 minutes
	rateLimitMaxRequests       = 5
)

var (
	rateMu     sync.Mutex
	rateLimits = make(map[string][]int64)
)

// AllowContactRequest returns false when the IP exceeded the contact rate limit.
func AllowContactRequest(ip string) bool {
	return allowRateLimited("contact:"+ip, rateLimitMaxRequests, rateLimitWindow)
}

// CheckRateLimit is a generic sliding-window limiter. Returns false when the
// (bucket,key) pair has already reached max within windowSec. Mirrors
// vault.checkRateLimit in the Express stack for auth/invite/sso flows.
func CheckRateLimit(bucket, key string, windowSec int64, max int) bool {
	if key == "" {
		key = "unknown"
	}
	return allowRateLimited(bucket+":"+key, max, windowSec)
}

func allowRateLimited(key string, max int, window int64) bool {
	now := time.Now().Unix()
	rateMu.Lock()
	defer rateMu.Unlock()

	stamps := rateLimits[key]
	fresh := stamps[:0]
	for _, t := range stamps {
		if now-t < window {
			fresh = append(fresh, t)
		}
	}
	if len(fresh) >= max {
		rateLimits[key] = fresh
		return false
	}
	fresh = append(fresh, now)
	rateLimits[key] = fresh
	return true
}
