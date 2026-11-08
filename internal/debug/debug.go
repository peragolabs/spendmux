package debug

import (
	"time"

	"github.com/peragolabs/spendmux/internal/actor"
	"github.com/peragolabs/spendmux/internal/auth"
	"github.com/peragolabs/spendmux/internal/core"
	"github.com/peragolabs/spendmux/internal/supervision"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func Crash(c *fiber.Ctx) error {
	uid, err := auth.GetUserID(c)
	if err != nil || uid == uuid.Nil {
		return core.JSON(c, fiber.StatusUnauthorized, fiber.Map{"error": "Not authenticated"})
	}

	_, err = supervision.Gateway().Send(actor.TypeDebugCrash, nil, 5*time.Second)
	if err == actor.ErrActorUnavailable {
		return core.JSON(c, fiber.StatusServiceUnavailable, fiber.Map{"error": "Actor unavailable"})
	}
	return core.JSON(c, fiber.StatusOK, fiber.Map{"ok": true})
}

func Stats(c *fiber.Ctx) error {
	uid, err := auth.GetUserID(c)
	if err != nil || uid == uuid.Nil {
		return core.JSON(c, fiber.StatusUnauthorized, fiber.Map{"error": "Not authenticated"})
	}

	restarts, status := supervision.Stats()
	return core.JSON(c, fiber.StatusOK, fiber.Map{
		"restarts": restarts,
		"status":   status,
	})
}
