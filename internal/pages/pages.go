package pages

import (
	"github.com/vrannang1/spendmux/internal/auth"
	"github.com/vrannang1/spendmux/internal/core"
	"os"
	"strings"

	"github.com/gofiber/fiber/v2"
)

func Home(c *fiber.Ctx) error {
	return core.ServeApp(c, "home")
}

func About(c *fiber.Ctx) error {
	return core.ServeApp(c, "about")
}

func Account(c *fiber.Ctx) error {
	if !auth.IsAuthenticated(c) {
		return c.Redirect("/login", fiber.StatusFound)
	}
	return core.ServeApp(c, "account")
}

// appPage serves a Spendmux app page: sign-in required.
func appPage(c *fiber.Ctx, appID string) error {
	if !auth.IsAuthenticated(c) {
		return c.Redirect("/login?next="+c.Path(), fiber.StatusFound)
	}
	return core.ServeApp(c, appID)
}

func Terms(c *fiber.Ctx) error   { return core.ServeApp(c, "terms") }
func Privacy(c *fiber.Ctx) error { return core.ServeApp(c, "privacy") }

// SiteConfig exposes public, non-secret settings the SPA needs to render
// (operator identity for legal pages, proxy base URL).
func SiteConfig(c *fiber.Ctx) error {
	entity := strings.TrimSpace(os.Getenv("LEGAL_ENTITY"))
	if entity == "" {
		entity = "Spendmux"
	}
	return c.JSON(fiber.Map{
		"operator":      entity,
		"support_email": strings.TrimSpace(os.Getenv("SUPPORT_EMAIL")),
		"base_url":      core.PublicAPIBase(c),
		"legal_updated": "September 17, 2026",
		"sign_in":       core.ConfiguredProviders(),
	})
}

func Keys(c *fiber.Ctx) error       { return appPage(c, "keys") }
func Fallback(c *fiber.Ctx) error   { return appPage(c, "fallback") }
func Usage(c *fiber.Ctx) error      { return appPage(c, "usage") }
func Playground(c *fiber.Ctx) error { return appPage(c, "playground") }
