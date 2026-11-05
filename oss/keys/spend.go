package keys

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vrannang1/spendmux/internal/database"
	"github.com/vrannang1/spendmux/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrDailyUSD      = errors.New("daily usd budget exceeded")
	ErrWeeklyUSD     = errors.New("weekly usd budget exceeded")
	ErrMonthlyUSD    = errors.New("monthly usd budget exceeded")
	ErrSessionBudget = errors.New("session usd budget exceeded")
	ErrTenantBudget  = errors.New("tenant usd budget exceeded")
	ErrTPMLimit      = errors.New("tokens per minute limit exceeded")
	// ErrPriceUnknown refuses a model Spendmux can't price on a token with a
	// USD cap: letting it through would spend money the cap never sees.
	ErrPriceUnknown = errors.New("no list price is known for this model, so a USD cap cannot be enforced")
)

const sessionWarnPercent = 80

// SpendRequest is the 429-before-egress input for one proxied call.
type SpendRequest struct {
	Key       models.PlatformAPIKey
	Tokens    int64
	USDMicros int64
	SessionID string
	TenantID  string
	LoopHash  string
	// PriceUnknown marks a call whose model has no list price. It is refused
	// when any USD cap (token, session, or tenant) would apply.
	PriceUnknown bool
	// RateTokens is the token estimate charged to the TPM rate limit. Tokens
	// is a hard upper bound for caps (prompt bytes + completion limit) and
	// would make TPM limits trip ~4x early. Zero means use Tokens.
	RateTokens int64
}

// SpendResult is returned when the request is allowed to leave the proxy.
type SpendResult struct {
	ReservationID uuid.UUID
	SessionWarn   bool
}

// EnforceSpendControls rate-limits the wallet, stops retry loops, and
// reserves token + USD budget (including session and tenant) before egress.
func EnforceSpendControls(req SpendRequest) (SpendResult, error) {
	rateTokens := req.RateTokens
	if rateTokens <= 0 {
		rateTokens = req.Tokens
	}
	if err := EnforceWalletRateLimits(req.Key, rateTokens); err != nil {
		return SpendResult{}, err
	}
	if err := EnforceLoopDetector(req.Key.ID, req.LoopHash); err != nil {
		return SpendResult{}, err
	}
	return ReserveSpend(req)
}

// ReserveSpend holds token and USD allowance on the virtual key. A limit of
// 0 means unlimited for that dimension. Session/tenant checks are skipped
// when the corresponding id is empty.
func ReserveSpend(req SpendRequest) (SpendResult, error) {
	pk := req.Key
	tokens := req.Tokens
	if tokens < 1 {
		tokens = 1
	}
	usd := req.USDMicros
	if usd < 0 {
		usd = 0
	}
	sessionID := strings.TrimSpace(req.SessionID)
	tenantID := strings.TrimSpace(req.TenantID)

	needsReserve := pk.DailyTokenLimit > 0 || pk.WeeklyTokenLimit > 0 ||
		pk.DailyUSDMicros > 0 || pk.WeeklyUSDMicros > 0 || pk.MonthlyUSDMicros > 0 ||
		(pk.SessionUSDMicros > 0 && sessionID != "") || tenantID != ""
	if !needsReserve {
		return SpendResult{}, nil
	}
	// A tenant header alone is attribution; ensureUSDBudgets refuses an
	// unknown price only when that tenant actually has a cap.
	if req.PriceUnknown && keyUSDCapped(pk, sessionID) {
		return SpendResult{}, ErrPriceUnknown
	}
	if usd < 1 && (pk.DailyUSDMicros > 0 || pk.WeeklyUSDMicros > 0 || pk.MonthlyUSDMicros > 0 || (pk.SessionUSDMicros > 0 && sessionID != "") || tenantID != "") {
		usd = 1
	}

	db := database.Get()
	now := time.Now().UTC()
	var reservation models.TokenReservation
	var warn bool
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
		if err := ensureUSDBudgets(tx, locked, usd, sessionID, tenantID, req.PriceUnknown, now); err != nil {
			return err
		}
		if locked.SessionUSDMicros > 0 && sessionID != "" {
			used, reserved, err := sumSessionUSD(tx, locked.ID, sessionID, now)
			if err != nil {
				return err
			}
			warn = sessionWouldWarn(used, reserved, usd, locked.SessionUSDMicros)
		}
		reservation = models.TokenReservation{
			PlatformAPIKeyID: pk.ID,
			Tokens:           tokens,
			USDMicros:        usd,
			SessionID:        sessionID,
			TenantID:         tenantID,
			ExpiresAt:        now.Add(reservationTTL),
			ChargeOnExpiry:   true,
		}
		return tx.Create(&reservation).Error
	})
	if err != nil {
		return SpendResult{}, err
	}
	return SpendResult{ReservationID: reservation.ID, SessionWarn: warn}, nil
}

func ensureUSDBudgets(tx *gorm.DB, pk models.PlatformAPIKey, pending int64, sessionID, tenantID string, priceUnknown bool, now time.Time) error {
	dayStart := utcDayStart(now)
	weekStart := utcWeekStart(now)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	if err := checkUSD(tx, pk.ID, "", "", pk.DailyUSDMicros, pending, dayStart, now, "today (UTC)", ErrDailyUSD); err != nil {
		return err
	}
	if err := checkUSD(tx, pk.ID, "", "", pk.WeeklyUSDMicros, pending, weekStart, now, "this week (UTC)", ErrWeeklyUSD); err != nil {
		return err
	}
	if err := checkUSD(tx, pk.ID, "", "", pk.MonthlyUSDMicros, pending, monthStart, now, "this month (UTC)", ErrMonthlyUSD); err != nil {
		return err
	}
	if pk.SessionUSDMicros > 0 && sessionID != "" {
		if err := checkUSD(tx, pk.ID, sessionID, "", pk.SessionUSDMicros, pending, time.Time{}, now, "this session", ErrSessionBudget); err != nil {
			return err
		}
	}
	if tenantID == "" {
		return nil
	}
	var cap models.TenantCap
	err := tx.Where("user_id = ? AND tenant = ?", pk.UserID, tenantID).First(&cap).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if cap.MonthlyUSDMicros <= 0 {
		return nil
	}
	if priceUnknown {
		return ErrPriceUnknown
	}
	// The tenant cap spans all of the account's tokens, but the transaction
	// only holds this token's row lock. Serialize per (account, tenant) so two
	// tokens cannot both pass the check and overshoot the cap together.
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", "tenant-cap:"+pk.UserID.String()+":"+tenantID).Error; err != nil {
		return err
	}
	return checkTenantUSD(tx, pk.UserID, tenantID, cap.MonthlyUSDMicros, pending, monthStart, now)
}

func checkUSD(tx *gorm.DB, keyID uuid.UUID, sessionID, tenantID string, limit, pending int64, start, now time.Time, label string, kind error) error {
	if limit <= 0 {
		return nil
	}
	used, reserved, err := sumWalletUSD(tx, keyID, sessionID, tenantID, start, now)
	if err != nil {
		return err
	}
	if wouldExceed(used, reserved, pending, limit) {
		return fmt.Errorf("%w: used %s, reserved %s, requested %s / %s %s",
			kind, formatUSD(used), formatUSD(reserved), formatUSD(pending), formatUSD(limit), label)
	}
	return nil
}

func checkTenantUSD(tx *gorm.DB, userID uuid.UUID, tenantID string, limit, pending int64, start, now time.Time) error {
	var used, reserved int64
	if err := tx.Model(&models.UsageEvent{}).
		Where("user_id = ? AND tenant_id = ? AND created_at >= ? AND ((status_code >= 200 AND status_code < 300) OR status_code = 499)", userID, tenantID, start).
		Select("COALESCE(SUM(cost_usd_micros),0)").Scan(&used).Error; err != nil {
		return err
	}
	if err := tx.Model(&models.TokenReservation{}).
		Where("tenant_id = ? AND expires_at > ? AND platform_api_key_id IN (?)",
			tenantID, now,
			tx.Model(&models.PlatformAPIKey{}).Select("id").Where("user_id = ?", userID)).
		Select("COALESCE(SUM(usd_micros),0)").Scan(&reserved).Error; err != nil {
		return err
	}
	if wouldExceed(used, reserved, pending, limit) {
		return fmt.Errorf("%w: used %s, reserved %s, requested %s / %s this month (UTC)",
			ErrTenantBudget, formatUSD(used), formatUSD(reserved), formatUSD(pending), formatUSD(limit))
	}
	return nil
}

func sumWalletUSD(tx *gorm.DB, keyID uuid.UUID, sessionID, tenantID string, start, now time.Time) (used, reserved int64, err error) {
	usage := tx.Model(&models.UsageEvent{}).
		Where("platform_api_key_id = ? AND ((status_code >= 200 AND status_code < 300) OR status_code = 499)", keyID)
	hold := tx.Model(&models.TokenReservation{}).
		Where("platform_api_key_id = ? AND expires_at > ?", keyID, now)
	if !start.IsZero() {
		usage = usage.Where("created_at >= ?", start)
	}
	if sessionID != "" {
		usage = usage.Where("session_id = ?", sessionID)
		hold = hold.Where("session_id = ?", sessionID)
	}
	if tenantID != "" {
		usage = usage.Where("tenant_id = ?", tenantID)
		hold = hold.Where("tenant_id = ?", tenantID)
	}
	if err = usage.Select("COALESCE(SUM(cost_usd_micros),0)").Scan(&used).Error; err != nil {
		return
	}
	if err = hold.Select("COALESCE(SUM(usd_micros),0)").Scan(&reserved).Error; err != nil {
		return
	}
	return
}

func sumSessionUSD(tx *gorm.DB, keyID uuid.UUID, sessionID string, now time.Time) (used, reserved int64, err error) {
	return sumWalletUSD(tx, keyID, sessionID, "", time.Time{}, now)
}

func wouldExceed(used, reserved, pending, limit int64) bool {
	if limit <= 0 {
		return false
	}
	return used+reserved+pending > limit
}

func sessionWouldWarn(used, reserved, pending, limit int64) bool {
	if limit <= 0 {
		return false
	}
	return (used+reserved+pending)*100 >= limit*sessionWarnPercent
}

func utcDayStart(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

func utcWeekStart(now time.Time) time.Time {
	dayStart := utcDayStart(now)
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	return dayStart.AddDate(0, 0, -(weekday - 1))
}

func formatUSD(micros int64) string {
	return fmt.Sprintf("$%.4f", float64(micros)/1_000_000)
}

// DollarsToMicros parses a dollar amount from a form field ("10", "3.5").
func DollarsToMicros(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
		return -1
	}
	rounded := math.Round(n * 1_000_000)
	if math.IsInf(rounded, 0) || rounded >= float64(math.MaxInt64) || (n > 0 && rounded < 1) {
		return -1
	}
	return int64(rounded)
}

// MicrosToDollars formats microdollars for the keys UI.
func MicrosToDollars(micros int64) string {
	if micros <= 0 {
		return "0"
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", float64(micros)/1_000_000), "0"), ".")
}

// UpsertTenantCap stores a monthly USD cap for a tenant. A cap of 0 deletes it.
func UpsertTenantCap(userID uuid.UUID, tenant string, monthlyUSDMicros int64) error {
	if monthlyUSDMicros < 0 {
		return fmt.Errorf("invalid dollar cap")
	}
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return fmt.Errorf("tenant is required")
	}
	db := database.Get()
	if monthlyUSDMicros <= 0 {
		return db.Unscoped().Where("user_id = ? AND tenant = ?", userID, tenant).Delete(&models.TenantCap{}).Error
	}
	var row models.TenantCap
	err := db.Where("user_id = ? AND tenant = ?", userID, tenant).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.Create(&models.TenantCap{UserID: userID, Tenant: tenant, MonthlyUSDMicros: monthlyUSDMicros}).Error
	}
	if err != nil {
		return err
	}
	return db.Model(&row).Update("monthly_usd_micros", monthlyUSDMicros).Error
}

// ListTenantCaps returns tenant caps for the keys page.
func ListTenantCaps(userID uuid.UUID) ([]models.TenantCap, error) {
	var rows []models.TenantCap
	err := database.Get().Where("user_id = ?", userID).Order("tenant asc").Find(&rows).Error
	return rows, err
}

// Receipt is the wallet / session / tenant spend snapshot vs list price.
type Receipt struct {
	WalletUSDMicros  int64 `json:"wallet_usd_micros"`
	SessionUSDMicros int64 `json:"session_usd_micros"`
	TenantUSDMicros  int64 `json:"tenant_usd_micros"`
	ListUSDMicros    int64 `json:"list_usd_micros"`
	CachedTokens     int64 `json:"cached_tokens"`
	ReasoningTokens  int64 `json:"reasoning_tokens"`
	Calls            int64 `json:"calls"`
}

// LoadReceipt sums BYOK list-price spend for the signed-in user.
func LoadReceipt(userID uuid.UUID, keyID *uuid.UUID, sessionID, tenantID string) (Receipt, error) {
	db := database.Get()
	monthStart := time.Date(time.Now().UTC().Year(), time.Now().UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	q := db.Model(&models.UsageEvent{}).
		Where("user_id = ? AND created_at >= ? AND ((status_code >= 200 AND status_code < 300) OR status_code = 499)", userID, monthStart)
	if keyID != nil && *keyID != uuid.Nil {
		q = q.Where("platform_api_key_id = ?", *keyID)
	}
	if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
		q = q.Where("session_id = ?", sessionID)
	}
	if tenantID = strings.TrimSpace(tenantID); tenantID != "" {
		q = q.Where("tenant_id = ?", tenantID)
	}
	var row struct {
		Wallet    int64
		List      int64
		Cached    int64
		Reasoning int64
		Calls     int64
	}
	if err := q.Select("COALESCE(SUM(cost_usd_micros),0) as wallet, COALESCE(SUM(list_cost_usd_micros),0) as list, COALESCE(SUM(cached_tokens),0) as cached, COALESCE(SUM(reasoning_tokens),0) as reasoning, COUNT(*) as calls").
		Scan(&row).Error; err != nil {
		return Receipt{}, err
	}
	out := Receipt{
		WalletUSDMicros: row.Wallet,
		ListUSDMicros:   row.List,
		CachedTokens:    row.Cached,
		ReasoningTokens: row.Reasoning,
		Calls:           row.Calls,
	}
	if out.ListUSDMicros == 0 {
		out.ListUSDMicros = out.WalletUSDMicros
	}
	if sessionID != "" {
		out.SessionUSDMicros = out.WalletUSDMicros
	}
	if tenantID != "" {
		out.TenantUSDMicros = out.WalletUSDMicros
	}
	if sessionID == "" && keyID != nil && *keyID != uuid.Nil {
		var sessionSum int64
		_ = db.Model(&models.UsageEvent{}).
			Where("user_id = ? AND platform_api_key_id = ? AND session_id <> '' AND created_at >= ? AND ((status_code >= 200 AND status_code < 300) OR status_code = 499)", userID, *keyID, monthStart).
			Select("COALESCE(SUM(cost_usd_micros),0)").Scan(&sessionSum)
		out.SessionUSDMicros = sessionSum
	}
	if tenantID == "" {
		var tenantSum int64
		tq := db.Model(&models.UsageEvent{}).
			Where("user_id = ? AND tenant_id <> '' AND created_at >= ? AND ((status_code >= 200 AND status_code < 300) OR status_code = 499)", userID, monthStart)
		if keyID != nil && *keyID != uuid.Nil {
			tq = tq.Where("platform_api_key_id = ?", *keyID)
		}
		_ = tq.Select("COALESCE(SUM(cost_usd_micros),0)").Scan(&tenantSum)
		out.TenantUSDMicros = tenantSum
	}
	return out, nil
}
