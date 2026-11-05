package keys

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vrannang1/spendmux/internal/database"
	"github.com/vrannang1/spendmux/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrReservationLost means a request's reservation is no longer active (it
// expired and was settled). The request must not start another paid attempt.
var ErrReservationLost = errors.New("budget reservation expired; request stopped before another upstream attempt")

func keyUSDCapped(pk models.PlatformAPIKey, sessionID string) bool {
	return pk.DailyUSDMicros > 0 || pk.WeeklyUSDMicros > 0 || pk.MonthlyUSDMicros > 0 ||
		(pk.SessionUSDMicros > 0 && sessionID != "")
}

// SpendCapsApply reports whether any hard cap (token or USD, including a
// session or tenant cap) governs this request. Callers use it to decide
// whether a request must carry a bounded completion limit. Uncapped requests
// are passed through unchanged.
func SpendCapsApply(pk models.PlatformAPIKey, sessionID, tenantID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	tenantID = strings.TrimSpace(tenantID)
	if pk.DailyTokenLimit > 0 || pk.WeeklyTokenLimit > 0 || keyUSDCapped(pk, sessionID) {
		return true
	}
	if tenantID == "" {
		return false
	}
	var count int64
	if err := database.Get().Model(&models.TenantCap{}).
		Where("user_id = ? AND tenant = ? AND monthly_usd_micros > 0", pk.UserID, tenantID).
		Count(&count).Error; err != nil {
		return true // fail closed: treat as capped
	}
	return count > 0
}

// GrowReservation raises an existing reservation to at least req.Tokens and
// req.USDMicros before another upstream attempt (a second key or a fallback
// hop), re-checking every cap for the increase. Usage recorded since the
// reservation was taken (for example an attempt whose outcome is unknown) is
// included, so a request cannot start a paid attempt the caps no longer
// allow. A nil id (no cap needed a reservation) is a no-op.
func GrowReservation(id uuid.UUID, req SpendRequest) error {
	if id == uuid.Nil {
		return nil
	}
	pk := req.Key
	sessionID := strings.TrimSpace(req.SessionID)
	tenantID := strings.TrimSpace(req.TenantID)
	if req.PriceUnknown && keyUSDCapped(pk, sessionID) {
		return ErrPriceUnknown
	}
	tokens := max(req.Tokens, 1)
	usd := max(req.USDMicros, 0)
	if usd < 1 && (keyUSDCapped(pk, sessionID) || tenantID != "") {
		usd = 1
	}
	now := time.Now().UTC()
	return database.Get().Transaction(func(tx *gorm.DB) error {
		var locked models.PlatformAPIKey
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, "id = ?", pk.ID).Error; err != nil {
			return err
		}
		if err := settleExpiredReservations(tx, now); err != nil {
			return err
		}
		var r models.TokenReservation
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&r, "id = ? AND platform_api_key_id = ? AND expires_at > ?", id, pk.ID, now).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrReservationLost
		}
		if err != nil {
			return err
		}
		addTokens := max(int64(0), tokens-r.Tokens)
		addUSD := max(int64(0), usd-r.USDMicros)
		if err := ensureBudgetWithReservations(tx, locked, addTokens, now); err != nil {
			return err
		}
		if err := ensureUSDBudgets(tx, locked, addUSD, r.SessionID, r.TenantID, req.PriceUnknown, now); err != nil {
			return err
		}
		if addTokens == 0 && addUSD == 0 {
			return nil
		}
		res := tx.Model(&models.TokenReservation{}).Where("id = ?", r.ID).
			Updates(map[string]any{"tokens": r.Tokens + addTokens, "usd_micros": r.USDMicros + addUSD})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return fmt.Errorf("grow reservation: %w", ErrReservationLost)
		}
		return nil
	})
}
