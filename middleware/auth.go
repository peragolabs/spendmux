package middleware

import (
	"github.com/vrannang1/spendmux/internal/auth"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// RequireAuth checks if a user is logged in
func RequireAuth(c *fiber.Ctx) error {
	userID, err := auth.GetUserID(c)

	if err != nil || userID == uuid.Nil {
		return c.Redirect("/login")
	}

	return c.Next()
}

// RequireAuthJSON is RequireAuth for JSON APIs: a 401 body instead of a
// redirect, so the SPA can route the user itself.
func RequireAuthJSON(c *fiber.Ctx) error {
	userID, err := auth.GetUserID(c)
	if err != nil || userID == uuid.Nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}
	c.Locals("user_id", userID.String())
	return c.Next()
}
