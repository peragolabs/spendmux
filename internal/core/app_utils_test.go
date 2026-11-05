package core

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestPublicAPIBase(t *testing.T) {
	for _, tc := range []struct{ env, want string }{
		{"", "http://spendmux.example"},
		{"https://api.spendmux.com", "https://api.spendmux.com"},
		{"https://api.spendmux.com/v1/", "https://api.spendmux.com"},
	} {
		t.Setenv("SPENDMUX_API_URL", tc.env)
		app := fiber.New()
		app.Get("/", func(c *fiber.Ctx) error { return c.SendString(PublicAPIBase(c)) })
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = "spendmux.example"
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		if got := string(body); got != tc.want {
			t.Errorf("SPENDMUX_API_URL=%q: got %q, want %q", tc.env, got, tc.want)
		}
	}
}
