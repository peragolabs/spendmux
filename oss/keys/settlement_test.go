package keys

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vrannang1/spendmux/internal/testdb"
	"github.com/vrannang1/spendmux/models"
)

func TestExpiredReservationPreservesUnrecordedSpendExactlyOnce(t *testing.T) {
	db := testdb.Setup(t)
	key := models.PlatformAPIKey{UserID: uuid.New(), Name: "recovery", KeyHash: uuid.NewString(), Prefix: "test", DailyTokenLimit: 100, DailyUSDMicros: 1000}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	reservation := models.TokenReservation{PlatformAPIKeyID: key.ID, Tokens: 100, USDMicros: 1000, ExpiresAt: time.Now().Add(-time.Minute), ChargeOnExpiry: true}
	if err := db.Create(&reservation).Error; err != nil {
		t.Fatal(err)
	}
	usage := models.UsageEvent{UserID: key.UserID, PlatformAPIKeyID: &key.ID, ReservationID: &reservation.ID, TotalTokens: 40, CostUSDMicros: 400, StatusCode: 200}
	if err := db.Create(&usage).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := CleanupExpiredTokenReservations(); err != nil {
			t.Fatal(err)
		}
	}
	var recovered models.UsageEvent
	if err := db.First(&recovered, "id = ?", reservation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if recovered.TotalTokens != 60 || recovered.CostUSDMicros != 600 || recovered.StatusCode != 499 {
		t.Fatalf("wrong recovery: %+v", recovered)
	}
	var count int64
	db.Model(&models.UsageEvent{}).Where("reservation_id = ?", reservation.ID).Count(&count)
	if count != 2 {
		t.Fatalf("got %d usage rows, want one original and one recovery", count)
	}
	db.Model(&models.TokenReservation{}).Where("id = ?", reservation.ID).Count(&count)
	if count != 0 {
		t.Fatal("settled reservation still active")
	}
	if _, err := ReserveSpend(SpendRequest{Key: key, Tokens: 1, USDMicros: 1}); !errors.Is(err, ErrDailyBudget) {
		t.Fatalf("expired reservation restored spending capacity: %v", err)
	}
}

func TestGrowReservationRechecksCapsForTheNextAttempt(t *testing.T) {
	db := testdb.Setup(t)
	key := models.PlatformAPIKey{UserID: uuid.New(), Name: "grow", KeyHash: uuid.NewString(), Prefix: "test", DailyUSDMicros: 1000}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	res, err := ReserveSpend(SpendRequest{Key: key, Tokens: 10, USDMicros: 400})
	if err != nil {
		t.Fatal(err)
	}
	// A pricier hop that still fits grows the hold in place.
	if err := GrowReservation(res.ReservationID, SpendRequest{Key: key, Tokens: 10, USDMicros: 900}); err != nil {
		t.Fatal(err)
	}
	var r models.TokenReservation
	db.First(&r, "id = ?", res.ReservationID)
	if r.USDMicros != 900 {
		t.Fatalf("reservation not grown: %d", r.USDMicros)
	}
	// One that does not fit is refused and leaves the hold unchanged.
	if err := GrowReservation(res.ReservationID, SpendRequest{Key: key, Tokens: 10, USDMicros: 1100}); !errors.Is(err, ErrDailyUSD) {
		t.Fatalf("over-cap hop allowed: %v", err)
	}
	// Usage recorded for an uncertain attempt counts before the next one.
	ev := models.UsageEvent{UserID: key.UserID, PlatformAPIKeyID: &key.ID, ReservationID: &r.ID, CostUSDMicros: 200, StatusCode: 499}
	if err := db.Create(&ev).Error; err != nil {
		t.Fatal(err)
	}
	if err := GrowReservation(res.ReservationID, SpendRequest{Key: key, Tokens: 10, USDMicros: 900}); !errors.Is(err, ErrDailyUSD) {
		t.Fatalf("recorded uncertain usage ignored: %v", err)
	}
	ReleaseTokenReservation(res.ReservationID)
	var left int64
	db.Unscoped().Model(&models.TokenReservation{}).Where("id = ?", res.ReservationID).Count(&left)
	if left != 0 {
		t.Fatal("released reservation must be removed, not soft-deleted")
	}
	if err := GrowReservation(res.ReservationID, SpendRequest{Key: key, Tokens: 1, USDMicros: 1}); !errors.Is(err, ErrReservationLost) {
		t.Fatalf("growing a released reservation: %v", err)
	}
}

func TestTenantHeaderWithoutCapAllowsUnpricedModel(t *testing.T) {
	db := testdb.Setup(t)
	key := models.PlatformAPIKey{UserID: uuid.New(), Name: "tenant", KeyHash: uuid.NewString(), Prefix: "test"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	res, err := ReserveSpend(SpendRequest{Key: key, Tokens: 10, TenantID: "acme", PriceUnknown: true})
	if err != nil {
		t.Fatalf("attribution-only tenant header refused an unpriced model: %v", err)
	}
	ReleaseTokenReservation(res.ReservationID)
	if SpendCapsApply(key, "", "acme") {
		t.Fatal("tenant without a cap reported as capped")
	}
	if err := UpsertTenantCap(key.UserID, "acme", 1_000_000); err != nil {
		t.Fatal(err)
	}
	if !SpendCapsApply(key, "", "acme") {
		t.Fatal("capped tenant not reported")
	}
	if _, err := ReserveSpend(SpendRequest{Key: key, Tokens: 10, TenantID: "acme", PriceUnknown: true}); !errors.Is(err, ErrPriceUnknown) {
		t.Fatalf("capped tenant accepted an unpriced model: %v", err)
	}
}

// Reservations left by the previous release have no reservation_id on their
// usage rows; charging them on expiry would double count, so they are dropped.
func TestLegacyReservationsAreDroppedNotCharged(t *testing.T) {
	db := testdb.Setup(t)
	key := models.PlatformAPIKey{UserID: uuid.New(), Name: "legacy", KeyHash: uuid.NewString(), Prefix: "test", DailyUSDMicros: 1000}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	legacy := models.TokenReservation{PlatformAPIKeyID: key.ID, Tokens: 100, USDMicros: 900, ExpiresAt: time.Now().Add(-time.Minute)}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := CleanupExpiredTokenReservations(); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&models.UsageEvent{}).Where("platform_api_key_id = ?", key.ID).Count(&n)
	if n != 0 {
		t.Fatal("legacy reservation charged as usage")
	}
	db.Unscoped().Model(&models.TokenReservation{}).Where("id = ?", legacy.ID).Count(&n)
	if n != 0 {
		t.Fatal("legacy reservation kept")
	}
}

func TestPurgeRemovesSoftDeletedReservations(t *testing.T) {
	db := testdb.Setup(t)
	key := models.PlatformAPIKey{UserID: uuid.New(), Name: "purge", KeyHash: uuid.NewString(), Prefix: "test"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	old := models.TokenReservation{PlatformAPIKeyID: key.ID, Tokens: 1, ExpiresAt: time.Now()}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	db.Delete(&old) // soft delete, as older releases did
	if _, err := PurgeStaleSpendState(); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Unscoped().Model(&models.TokenReservation{}).Where("id = ?", old.ID).Count(&n)
	if n != 0 {
		t.Fatal("soft-deleted reservation not purged")
	}
}
