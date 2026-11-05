package middleware

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestProductionSafetyRejectsCrossOriginMutation(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("BASE_URL", "https://spendmux.example")
	app := fiber.New()
	app.Use(ProductionSafety())
	app.Post("/account/delete", func(c *fiber.Ctx) error { return c.SendStatus(204) })
	req := httptest.NewRequest("POST", "/account/delete", nil)
	req.Header.Set("Origin", "https://evil.example")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestProductionSafetyAllowsBearerAPIWithoutOrigin(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("BASE_URL", "https://spendmux.example")
	app := fiber.New()
	app.Use(ProductionSafety())
	app.Post("/v1/chat/completions", func(c *fiber.Ctx) error { return c.SendStatus(204) })
	resp, err := app.Test(httptest.NewRequest("POST", "/v1/chat/completions", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Strict-Transport-Security") == "" || resp.Header.Get("X-Request-ID") == "" {
		t.Fatal("expected security and request ID headers")
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if csp == "" || !strings.Contains(csp, "font-src 'self' data:") {
		t.Fatalf("expected font-src to allow data: URLs, got %q", csp)
	}
}

func TestRequireWebSocketOriginRejectsCrossOrigin(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("BASE_URL", "https://spendmux.example")
	app := fiber.New()
	app.Get("/ws/chat", RequireWebSocketOrigin, func(c *fiber.Ctx) error { return c.SendStatus(204) })
	req := httptest.NewRequest("GET", "/ws/chat", nil)
	req.Header.Set("Origin", "https://evil.example")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestRequireJSON(t *testing.T) {
	app := fiber.New()
	app.Post("/json", RequireJSON, func(c *fiber.Ctx) error { return c.SendStatus(204) })
	bad := httptest.NewRequest("POST", "/json", nil)
	bad.Header.Set("Content-Type", "application/xml")
	resp, err := app.Test(bad)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d", resp.StatusCode)
	}
	good := httptest.NewRequest("POST", "/json", nil)
	good.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err = app.Test(good)
	if err != nil || resp.StatusCode != fiber.StatusNoContent {
		t.Fatalf("expected JSON request to pass, status=%d err=%v", resp.StatusCode, err)
	}
}
