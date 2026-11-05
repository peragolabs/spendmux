package keys

import (
	"github.com/vrannang1/spendmux/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

// An abandoned/uncertain request must not silently recover spend capacity.
// Convert only the unrecorded part of its admitted bound to an explicit
// estimated usage row. Stable IDs make concurrent recovery idempotent.
func settleExpiredReservations(tx *gorm.DB, now time.Time) error {
	var pending []models.TokenReservation
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("expires_at <= ?", now).Order("id").Find(&pending).Error; err != nil {
		return err
	}
	for _, reservation := range pending {
		if !reservation.ChargeOnExpiry {
			if err := tx.Unscoped().Delete(&reservation).Error; err != nil {
				return err
			}
			continue
		}
		var key models.PlatformAPIKey
		if err := tx.Unscoped().First(&key, "id = ?", reservation.PlatformAPIKeyID).Error; err != nil {
			return err
		}
		var recorded struct {
			Tokens int64
			Cost   int64
		}
		if err := tx.Model(&models.UsageEvent{}).Where("reservation_id = ?", reservation.ID).
			Select("COALESCE(SUM(total_tokens),0) AS tokens, COALESCE(SUM(cost_usd_micros),0) AS cost").Scan(&recorded).Error; err != nil {
			return err
		}
		tokens := max(int64(0), reservation.Tokens-recorded.Tokens)
		cost := max(int64(0), reservation.USDMicros-recorded.Cost)
		if tokens > 0 || cost > 0 {
			event := models.UsageEvent{
				Base:   models.Base{ID: reservation.ID, CreatedAt: reservation.CreatedAt},
				UserID: key.UserID, PlatformAPIKeyID: &reservation.PlatformAPIKeyID,
				ReservationID: &reservation.ID, Provider: "reservation-recovery", Model: "unknown",
				Endpoint: "reconciliation", TotalTokens: int(tokens), CostUSDMicros: cost, ListCostUSDMicros: cost,
				SessionID: reservation.SessionID, TenantID: reservation.TenantID, StatusCode: 499,
				ErrorMessage: "Request ended without verified settlement; unrecorded reserved upper bound counted against caps. Reconcile with provider usage.",
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&event).Error; err != nil {
				return err
			}
		}
		if err := tx.Unscoped().Delete(&reservation).Error; err != nil {
			return err
		}
	}
	return nil
}
