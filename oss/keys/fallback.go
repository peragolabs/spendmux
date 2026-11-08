package keys

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/peragolabs/spendmux/internal/auth"
	"github.com/peragolabs/spendmux/internal/core"
	"github.com/peragolabs/spendmux/internal/database"
	"github.com/peragolabs/spendmux/internal/providers"
	"github.com/peragolabs/spendmux/models"
)

// LoadFallbackSteps returns the user's ordered fallback ladder (may be empty).
func LoadFallbackSteps(userID uuid.UUID) ([]models.FallbackStep, error) {
	var row models.FallbackChain
	err := database.Get().Where("user_id = ?", userID).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if strings.TrimSpace(row.StepsJSON) == "" || row.StepsJSON == "null" {
		return nil, nil
	}
	var steps []models.FallbackStep
	if err := json.Unmarshal([]byte(row.StepsJSON), &steps); err != nil {
		return nil, err
	}
	return steps, nil
}

func saveFallbackSteps(userID uuid.UUID, steps []models.FallbackStep) error {
	raw, err := json.Marshal(steps)
	if err != nil {
		return err
	}
	db := database.Get()
	var row models.FallbackChain
	err = db.Where("user_id = ?", userID).First(&row).Error
	if err != nil {
		return db.Create(&models.FallbackChain{
			UserID:    userID,
			StepsJSON: string(raw),
		}).Error
	}
	row.StepsJSON = string(raw)
	return db.Save(&row).Error
}

func fallbackFlash(code string) string {
	switch code {
	case "unknown_provider":
		return "Unknown provider"
	case "fallback_model_required":
		return "Fallback step needs a model id"
	case "fallback_too_long":
		return "Fallback chain max is 8 steps"
	case "fallback_no_key":
		return "Save a key for that provider on /keys before adding it as a hop"
	case "fallback_duplicate":
		return "That provider + model is already on the ladder"
	case "save_failed":
		return "Could not save fallback ladder"
	default:
		return code
	}
}

func fallbackStepRows(steps []models.FallbackStep) []fiber.Map {
	rows := make([]fiber.Map, 0, len(steps))
	for i, s := range steps {
		name := s.Provider
		if p, ok := providers.Get(s.Provider); ok {
			name = p.Name
		}
		rows = append(rows, fiber.Map{
			"Index":    i,
			"Position": i + 1,
			"Provider": s.Provider,
			"Name":     name,
			"Model":    s.Model,
		})
	}
	return rows
}

// FallbackPageHandler renders /fallback.
func FallbackPageHandler(c *fiber.Ctx) error {
	userID, err := auth.GetUserID(c)
	if err != nil || userID == uuid.Nil {
		return c.Redirect("/auth")
	}
	db := database.Get()
	var user models.User
	if err := db.First(&user, "id = ?", userID).Error; err != nil {
		return c.Redirect("/")
	}
	steps, _ := LoadFallbackSteps(userID)
	errMsg := c.Query("error")
	if errMsg != "" {
		errMsg = fallbackFlash(errMsg)
	}
	return c.JSON(fiber.Map{
		"Title":          "Fallback",
		"User":           user,
		"ProviderGroups": providers.Groups(),
		"SavedProviders": savedProviderRows(userID),
		"FallbackSteps":  fallbackStepRows(steps),
		"Error":          errMsg,
		"BaseURL":        core.PublicAPIBase(c),
	})
}

func savedProviderRows(userID uuid.UUID) []fiber.Map {
	var pkeys []models.ProviderKey
	database.Get().Where("user_id = ?", userID).Order("provider asc").Find(&pkeys)
	rows := make([]fiber.Map, 0, len(pkeys))
	seen := map[string]bool{}
	for _, k := range pkeys {
		if seen[k.Provider] { // several keys for one provider are one option
			continue
		}
		seen[k.Provider] = true
		name := k.Provider
		if p, ok := providers.Get(k.Provider); ok {
			name = p.Name
		}
		rows = append(rows, fiber.Map{
			"ID":   k.Provider,
			"Name": name,
		})
	}
	return rows
}

// AddFallbackStepHandler appends a provider+model hop to the ladder.
func AddFallbackStepHandler(c *fiber.Ctx) error {
	userID, err := auth.GetUserID(c)
	if err != nil || userID == uuid.Nil {
		return c.Redirect("/auth")
	}
	provider := strings.ToLower(strings.TrimSpace(c.FormValue("provider")))
	model := strings.TrimSpace(c.FormValue("model"))
	if _, ok := providers.Get(provider); !ok {
		return finish(c, "/fallback?error=unknown_provider")
	}
	if model == "" {
		return finish(c, "/fallback?error=fallback_model_required")
	}
	// Only providers with a saved key can be a hop — otherwise the ladder
	// silently skips it at request time and the user wonders why.
	var keyCount int64
	database.Get().Model(&models.ProviderKey{}).Where("user_id = ? AND provider = ?", userID, provider).Count(&keyCount)
	if keyCount == 0 {
		return finish(c, "/fallback?error=fallback_no_key")
	}

	steps, _ := LoadFallbackSteps(userID)
	if steps == nil {
		steps = []models.FallbackStep{}
	}
	if len(steps) >= 8 {
		return finish(c, "/fallback?error=fallback_too_long")
	}
	for _, st := range steps {
		if st.Provider == provider && st.Model == model {
			return finish(c, "/fallback?error=fallback_duplicate")
		}
	}
	steps = append(steps, models.FallbackStep{Provider: provider, Model: model})
	if err := saveFallbackSteps(userID, steps); err != nil {
		return finish(c, "/fallback?error=save_failed")
	}
	return finish(c, "/fallback")
}

// DeleteFallbackStepHandler removes one hop by index.
func DeleteFallbackStepHandler(c *fiber.Ctx) error {
	userID, err := auth.GetUserID(c)
	if err != nil || userID == uuid.Nil {
		return c.Redirect("/auth")
	}
	idx, err := strconv.Atoi(c.Params("index"))
	if err != nil || idx < 0 {
		return finish(c, "/fallback")
	}
	steps, err := LoadFallbackSteps(userID)
	if err != nil || idx >= len(steps) {
		return finish(c, "/fallback")
	}
	steps = append(steps[:idx], steps[idx+1:]...)
	_ = saveFallbackSteps(userID, steps)
	return finish(c, "/fallback")
}

// ClearFallbackHandler wipes the ladder.
func ClearFallbackHandler(c *fiber.Ctx) error {
	userID, err := auth.GetUserID(c)
	if err != nil || userID == uuid.Nil {
		return c.Redirect("/auth")
	}
	_ = saveFallbackSteps(userID, []models.FallbackStep{})
	return finish(c, "/fallback")
}
