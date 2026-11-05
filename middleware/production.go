package middleware

import (
	"log/slog"
	"mime"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// ProductionSafety adds correlation IDs, defensive browser headers, compact
// request logs, and same-origin protection for cookie-authenticated mutations.
func ProductionSafety() fiber.Handler {
	return func(c *fiber.Ctx) error {
		requestID := strings.TrimSpace(c.Get("X-Request-ID"))
		if len(requestID) < 8 || len(requestID) > 128 || strings.ContainsAny(requestID, "\r\n") {
			requestID = uuid.NewString()
		}
		c.Set("X-Request-ID", requestID)
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("X-Frame-Options", "DENY")
		c.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		c.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' data: https://fonts.gstatic.com; img-src 'self' data: https://ui-avatars.com; connect-src 'self' ws: wss:; object-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'; upgrade-insecure-requests")
		if strings.EqualFold(os.Getenv("APP_ENV"), "production") {
			c.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		if err := requireSameOrigin(c); err != nil {
			return err
		}
		started := time.Now()
		err := c.Next()
		slog.Info("http_request",
			"request_id", requestID,
			"method", c.Method(),
			"path", c.Path(),
			"status", c.Response().StatusCode(),
			"duration_ms", time.Since(started).Milliseconds(),
		)
		return err
	}
}

// RequireJSON rejects content-type confusion on JSON-only API routes.
func RequireJSON(c *fiber.Ctx) error {
	mediaType, _, err := mime.ParseMediaType(c.Get(fiber.HeaderContentType))
	if err != nil || mediaType != fiber.MIMEApplicationJSON {
		return fiber.NewError(fiber.StatusUnsupportedMediaType, "Content-Type must be application/json")
	}
	return c.Next()
}

// RequireWebSocketOrigin protects cookie-authenticated WebSocket upgrades from
// cross-site WebSocket hijacking. Browsers always send Origin on upgrades.
func RequireWebSocketOrigin(c *fiber.Ctx) error {
	origin := strings.TrimSpace(c.Get("Origin"))
	if origin == "" {
		if strings.EqualFold(os.Getenv("APP_ENV"), "production") {
			return fiber.NewError(fiber.StatusForbidden, "websocket origin required")
		}
		return c.Next()
	}
	u, err := url.Parse(origin)
	if err != nil || !strings.EqualFold(u.Host, expectedHost(c)) {
		return fiber.NewError(fiber.StatusForbidden, "cross-origin websocket rejected")
	}
	return c.Next()
}

func requireSameOrigin(c *fiber.Ctx) error {
	switch c.Method() {
	case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
		return nil
	}
	if strings.HasPrefix(c.Path(), "/v1/") || c.Path() == "/webhook" || strings.HasPrefix(c.Path(), "/webhooks/") {
		return nil
	}
	// This exact callback authenticates the provider response and consumes a
	// single-use state bound to an HttpOnly browser cookie in auth.Callback.
	if c.Method() == fiber.MethodPost && c.Path() == "/auth/apple/callback" {
		return nil
	}
	origin := strings.TrimSpace(c.Get("Origin"))
	if origin == "" {
		origin = strings.TrimSpace(c.Get("Referer"))
	}
	if origin == "" {
		if strings.EqualFold(os.Getenv("APP_ENV"), "production") {
			return fiber.NewError(fiber.StatusForbidden, "origin required")
		}
		return nil
	}
	u, err := url.Parse(origin)
	if err != nil || !strings.EqualFold(u.Host, expectedHost(c)) {
		return fiber.NewError(fiber.StatusForbidden, "cross-origin request rejected")
	}
	return nil
}

func expectedHost(c *fiber.Ctx) string {
	if configured, err := url.Parse(strings.TrimSpace(os.Getenv("BASE_URL"))); err == nil && configured.Host != "" {
		return configured.Host
	}
	return c.Hostname()
}
