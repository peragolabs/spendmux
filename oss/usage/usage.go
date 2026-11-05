package usage

import (
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/vrannang1/spendmux/internal/auth"
	"github.com/vrannang1/spendmux/internal/database"
	"github.com/vrannang1/spendmux/models"
)

// PageHandler returns this month's totals, spend by provider, and the
// latest requests from logged usage events.
func PageHandler(c *fiber.Ctx) error {
	userID, err := auth.GetUserID(c)
	if err != nil || userID == uuid.Nil {
		return c.Redirect("/auth")
	}

	db := database.Get()
	var user models.User
	if err := db.First(&user, "id = ?", userID).Error; err != nil {
		return c.Redirect("/")
	}

	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	var monthCost, monthTokens int64
	var monthReqs int64
	db.Model(&models.UsageEvent{}).
		Where("user_id = ? AND created_at >= ?", userID, monthStart).
		Select("COALESCE(SUM(cost_usd_micros),0)").Scan(&monthCost)
	db.Model(&models.UsageEvent{}).
		Where("user_id = ? AND created_at >= ?", userID, monthStart).
		Select("COALESCE(SUM(total_tokens),0)").Scan(&monthTokens)
	db.Model(&models.UsageEvent{}).
		Where("user_id = ? AND created_at >= ?", userID, monthStart).
		Count(&monthReqs)

	type providerRow struct {
		Provider     string
		Requests     int64
		PromptTokens int64
		Completion   int64
		CostMicros   int64
		AvgLatencyMs float64
	}
	var byProvider []providerRow
	db.Model(&models.UsageEvent{}).
		Select(`provider,
			COUNT(*) as requests,
			COALESCE(SUM(prompt_tokens),0) as prompt_tokens,
			COALESCE(SUM(completion_tokens),0) as completion,
			COALESCE(SUM(cost_usd_micros),0) as cost_micros,
			COALESCE(AVG(latency_ms),0) as avg_latency_ms`).
		Where("user_id = ? AND created_at >= ?", userID, monthStart).
		Group("provider").
		Order("cost_micros desc").
		Scan(&byProvider)

	type provView struct {
		Provider     string
		Requests     int64
		PromptTokens int64
		Completion   int64
		Cost         string
		AvgLatency   string
	}
	provViews := make([]provView, 0, len(byProvider))
	for _, p := range byProvider {
		provViews = append(provViews, provView{
			Provider:     p.Provider,
			Requests:     p.Requests,
			PromptTokens: p.PromptTokens,
			Completion:   p.Completion,
			Cost:         formatUSD(p.CostMicros),
			AvgLatency:   fmt.Sprintf("%.0fms", p.AvgLatencyMs),
		})
	}

	var recent []models.UsageEvent
	db.Where("user_id = ?", userID).Order("created_at desc").Limit(50).Find(&recent)

	type recentRow struct {
		When     string
		Provider string
		Model    string
		Endpoint string
		Tokens   int
		Cost     string
		Latency  string
		Status   int
	}
	recents := make([]recentRow, 0, len(recent))
	for _, e := range recent {
		recents = append(recents, recentRow{
			When:     e.CreatedAt.Local().Format("Jan 2 15:04"),
			Provider: e.Provider,
			Model:    e.Model,
			Endpoint: e.Endpoint,
			Tokens:   e.TotalTokens,
			Cost:     formatUSD(e.CostUSDMicros),
			Latency:  fmt.Sprintf("%dms", e.LatencyMs),
			Status:   e.StatusCode,
		})
	}

	return c.JSON(fiber.Map{
		"Title":       "Usage",
		"User":        user,
		"MonthLabel":  now.Format("January 2006"),
		"MonthCost":   formatUSD(monthCost),
		"MonthReqs":   monthReqs,
		"MonthTokens": monthTokens,
		"ByProvider":  provViews,
		"Recent":      recents,
	})
}

func formatUSD(micros int64) string {
	return fmt.Sprintf("$%.4f", float64(micros)/1e6)
}
