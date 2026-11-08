package keys

import (
	"errors"
	"fmt"
	"time"

	"github.com/peragolabs/spendmux/internal/database"
	"github.com/peragolabs/spendmux/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrDailyBudget  = errors.New("daily token budget exceeded")
	ErrWeeklyBudget = errors.New("weekly token budget exceeded")
)

// EnsureTokenBudget hard-stops when this virtual key's day/week token sum is at/over limit.
// Limits of 0 mean unlimited.
func EnsureTokenBudget(pk models.PlatformAPIKey) error {
	if pk.DailyTokenLimit <= 0 && pk.WeeklyTokenLimit <= 0 {
		return nil
	}
	db := database.Get()
	now := time.Now().UTC()

	if pk.DailyTokenLimit > 0 {
		dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		var used int64
		_ = db.Model(&models.UsageEvent{}).
			Where("platform_api_key_id = ? AND created_at >= ? AND ((status_code >= 200 AND status_code < 300) OR status_code = 499) AND cache_hit IS NOT TRUE", pk.ID, dayStart).
			Select("COALESCE(SUM(total_tokens),0)").Scan(&used)
		if used >= pk.DailyTokenLimit {
			return fmt.Errorf("%w: used %d / %d tokens today (UTC)", ErrDailyBudget, used, pk.DailyTokenLimit)
		}
	}

	if pk.WeeklyTokenLimit > 0 {
		weekday := int(now.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		weekStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -(weekday - 1))
		var used int64
		_ = db.Model(&models.UsageEvent{}).
			Where("platform_api_key_id = ? AND created_at >= ? AND ((status_code >= 200 AND status_code < 300) OR status_code = 499) AND cache_hit IS NOT TRUE", pk.ID, weekStart).
			Select("COALESCE(SUM(total_tokens),0)").Scan(&used)
		if used >= pk.WeeklyTokenLimit {
			return fmt.Errorf("%w: used %d / %d tokens this week (UTC)", ErrWeeklyBudget, used, pk.WeeklyTokenLimit)
		}
	}
	return nil
}

// Exceeds the upstream client's 30 minute hard timeout, including settlement.
const reservationTTL = 35 * time.Minute

// ReserveTokenBudget atomically reserves a conservative allowance before a
// request leaves the proxy, preventing concurrent requests from overspending.
func ReserveTokenBudget(pk models.PlatformAPIKey, tokens int64) (uuid.UUID, error) {
	if pk.DailyTokenLimit <= 0 && pk.WeeklyTokenLimit <= 0 {
		return uuid.Nil, nil
	}
	if tokens < 1 {
		tokens = 1
	}
	db := database.Get()
	now := time.Now().UTC()
	var reservation models.TokenReservation
	err := db.Transaction(func(tx *gorm.DB) error {
		var locked models.PlatformAPIKey
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, "id = ?", pk.ID).Error; err != nil {
			return err
		}
		if err := settleExpiredReservations(tx, now); err != nil {
			return err
		}
		if err := ensureBudgetWithReservations(tx, locked, tokens, now); err != nil {
			return err
		}
		reservation = models.TokenReservation{PlatformAPIKeyID: pk.ID, Tokens: tokens, ExpiresAt: now.Add(reservationTTL), ChargeOnExpiry: true}
		return tx.Create(&reservation).Error
	})
	if err != nil {
		return uuid.Nil, err
	}
	return reservation.ID, nil
}

// ReleaseTokenReservation frees an allowance after its usage event is durable.
func ReleaseTokenReservation(id uuid.UUID) {
	if id != uuid.Nil {
		// Hard delete: soft-deleted reservations were never read again and
		// grew without bound (one row per capped request).
		_ = database.Get().Unscoped().Where("id = ?", id).Delete(&models.TokenReservation{}).Error
	}
}

func CleanupExpiredTokenReservations() error {
	return database.Get().Transaction(func(tx *gorm.DB) error { return settleExpiredReservations(tx, time.Now().UTC()) })
}

func ensureBudgetWithReservations(db *gorm.DB, pk models.PlatformAPIKey, pending int64, now time.Time) error {
	check := func(limit int64, start time.Time, label string, kind error) error {
		if limit <= 0 {
			return nil
		}
		var used, reserved int64
		if err := db.Model(&models.UsageEvent{}).
			Where("platform_api_key_id = ? AND created_at >= ? AND ((status_code >= 200 AND status_code < 300) OR status_code = 499) AND cache_hit IS NOT TRUE", pk.ID, start).
			Select("COALESCE(SUM(total_tokens),0)").Scan(&used).Error; err != nil {
			return err
		}
		if err := db.Model(&models.TokenReservation{}).
			Where("platform_api_key_id = ? AND expires_at > ?", pk.ID, now).
			Select("COALESCE(SUM(tokens),0)").Scan(&reserved).Error; err != nil {
			return err
		}
		if used+reserved+pending > limit {
			return fmt.Errorf("%w: used %d, reserved %d, requested %d / %d tokens %s", kind, used, reserved, pending, limit, label)
		}
		return nil
	}
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if err := check(pk.DailyTokenLimit, dayStart, "today (UTC)", ErrDailyBudget); err != nil {
		return err
	}
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	weekStart := dayStart.AddDate(0, 0, -(weekday - 1))
	return check(pk.WeeklyTokenLimit, weekStart, "this week (UTC)", ErrWeeklyBudget)
}

// TokenUsageWindow returns tokens used for a key in day and week windows (for UI).
func TokenUsageWindow(keyID uuid.UUID) (dayUsed, weekUsed int64) {
	return sumKeyTokens(keyID)
}
