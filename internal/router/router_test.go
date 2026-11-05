package router

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func clientIP(t *testing.T, header string) string {
	t.Helper()
	app := fiber.New(appConfig())
	app.Get("/ip", func(c *fiber.Ctx) error { return c.SendString(c.IP()) })
	req := httptest.NewRequest("GET", "/ip", nil)
	req.Header.Set("X-Real-Ip", header)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestClientIPHeaderTrustedOnlyFromProxies(t *testing.T) {
	// app.Test connects from 0.0.0.0, which is not a private proxy address.
	if ip := clientIP(t, "203.0.113.9"); ip == "203.0.113.9" {
		t.Fatal("a forwarding header from an untrusted address must be ignored")
	}
	t.Setenv("TRUSTED_PROXIES", "0.0.0.0/32")
	if ip := clientIP(t, "203.0.113.9"); ip != "203.0.113.9" {
		t.Fatalf("header from a trusted proxy ignored: got %q", ip)
	}
}

func TestDrainingReplicaIsNotReady(t *testing.T) {
	defer draining.Store(false)
	BeginDrain()
	app := fiber.New()
	app.Get("/health/ready", ready)
	resp, err := app.Test(httptest.NewRequest("GET", "/health/ready", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 while draining", resp.StatusCode)
	}
}

func TestServerHasReadAndIdleTimeoutsButNoWriteTimeout(t *testing.T) {
	cfg := appConfig()
	if cfg.ReadTimeout <= 0 || cfg.IdleTimeout <= 0 {
		t.Fatalf("missing timeouts: %+v", cfg)
	}
	if cfg.WriteTimeout != 0 {
		t.Fatal("a write timeout would cut long streamed completions")
	}
	t.Setenv("HTTP_READ_TIMEOUT_SECONDS", "15")
	if appConfig().ReadTimeout.Seconds() != 15 {
		t.Fatal("HTTP_READ_TIMEOUT_SECONDS ignored")
	}
}
