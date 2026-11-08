package router

import (
	"context"
	"github.com/peragolabs/spendmux/internal/telemetry"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/peragolabs/spendmux/internal/auth"
	"github.com/peragolabs/spendmux/internal/core"
	"github.com/peragolabs/spendmux/internal/database"
	"github.com/peragolabs/spendmux/internal/debug"
	"github.com/peragolabs/spendmux/internal/observability"
	"github.com/peragolabs/spendmux/internal/pages"
	"github.com/peragolabs/spendmux/internal/playground"
	"github.com/peragolabs/spendmux/internal/supervision"
	"github.com/peragolabs/spendmux/middleware"
	"github.com/peragolabs/spendmux/oss/keys"
	"github.com/peragolabs/spendmux/oss/proxy"
	"github.com/peragolabs/spendmux/oss/usage"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/websocket/v2"
	"github.com/google/uuid"
)

func appConfig() fiber.Config {
	return fiber.Config{
		DisableStartupMessage: true,
		BodyLimit:             10 * 1024 * 1024,
		ServerHeader:          "Spendmux",
		// c.IP() is the real client only when the request came through a
		// trusted proxy (Traefik on the private network); anyone else's
		// forwarding header is ignored, so rate limits can't be dodged or shared.
		ProxyHeader:             proxyHeader(),
		EnableTrustedProxyCheck: true,
		TrustedProxies:          trustedProxies(),
		EnableIPValidation:      true,
		// Slow clients cannot hold connections open forever. There is no
		// WriteTimeout: streamed completions legitimately run for minutes
		// (bounded by the 30 minute upstream deadline instead).
		ReadTimeout: envDuration("HTTP_READ_TIMEOUT_SECONDS", 60*time.Second),
		IdleTimeout: envDuration("HTTP_IDLE_TIMEOUT_SECONDS", 120*time.Second),
	}
}

func envDuration(name string, def time.Duration) time.Duration {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil && v > 0 && v <= 3600 {
		return time.Duration(v) * time.Second
	}
	return def
}

// proxyHeader names the header the reverse proxy puts the client IP in.
// Traefik sets X-Real-Ip to the connecting address itself.
func proxyHeader() string {
	if h := strings.TrimSpace(os.Getenv("TRUSTED_PROXY_HEADER")); h != "" {
		return h
	}
	return "X-Real-Ip"
}

// trustedProxies lists the addresses allowed to set it: TRUSTED_PROXIES
// (comma-separated IPs or CIDRs), or private networks and loopback, where a
// Docker or Kubernetes ingress lives.
func trustedProxies() []string {
	if v := strings.TrimSpace(os.Getenv("TRUSTED_PROXIES")); v != "" {
		var out []string
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return []string{"127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7"}
}

// draining is set on shutdown so /health/ready fails and the load balancer
// stops sending new requests while in-flight ones (streams) finish.
var draining atomic.Bool

// BeginDrain marks the replica not ready.
func BeginDrain() { draining.Store(true) }

func ready(c *fiber.Ctx) error {
	if draining.Load() {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"status": "draining"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := database.Ping(ctx); err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"status": "not_ready", "database": "unavailable"})
	}
	if supervision.Gateway() == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"status": "not_ready", "actor": "unavailable"})
	}
	return c.JSON(fiber.Map{"status": "ready"})
}

// Setup registers all routes.
func Setup() *fiber.App {
	app := fiber.New(appConfig())

	app.Use(recover.New())
	// Request ids, security headers, same-origin checks on cookie-authenticated
	// mutations (bearer /v1 exempt), compact request logs.
	app.Use(middleware.ProductionSafety())
	app.Use(observability.HTTPMetrics())
	app.Use(middleware.RateLimitMiddleware())

	// ── Ops ───────────────────────────────────────────────────
	app.Get("/health/live", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})
	app.Get("/health/ready", ready)
	app.Get("/metrics", observability.Metrics)

	// ── OpenAI-compatible proxy: bearer token auth inside each handler ──
	v1 := app.Group("/v1", telemetry.Middleware(), proxy.InFlightGuard)
	v1.Post("/chat/completions", middleware.RequireJSON, proxy.V1ChatCompletions)
	v1.Post("/messages", middleware.RequireJSON, proxy.V1Messages)
	v1.Post("/responses", middleware.RequireJSON, proxy.V1Responses)
	v1.Post("/embeddings", middleware.RequireJSON, proxy.V1Embeddings)
	v1.Post("/images/generations", middleware.RequireJSON, proxy.V1ImagesGenerations)
	v1.Post("/audio/speech", middleware.RequireJSON, proxy.V1AudioSpeech)
	v1.Post("/audio/transcriptions", proxy.V1AudioTranscriptions)
	v1.Post("/moderations", middleware.RequireJSON, proxy.V1Moderations)
	v1.Get("/models", proxy.V1Models)
	v1.Delete("/memory/:thread", proxy.V1DeleteMemory)

	// ── Pages ─────────────────────────────────────────────────
	app.Get("/", pages.Home)
	app.Get("/about", pages.About)
	app.Get("/login", auth.LoginPage)
	app.Get("/account", pages.Account)
	app.Get("/terms", pages.Terms)
	app.Get("/privacy", pages.Privacy)
	app.Get("/api/site", pages.SiteConfig)
	app.Get("/keys", pages.Keys)
	app.Get("/fallback", pages.Fallback)
	app.Get("/usage", pages.Usage)
	app.Get("/chat", pages.Playground)

	// ── OAuth ─────────────────────────────────────────────────
	app.Get("/auth/:provider", auth.RedirectToProvider)
	app.Get("/auth/:provider/callback", auth.OAuthCallback)
	app.Post("/auth/:provider/callback", auth.OAuthCallback)
	// Legacy callback path already registered in provider consoles.
	app.Get("/oauth/:provider/callback", auth.OAuthCallback)
	app.Get("/auth/:provider/link", auth.RedirectToProviderLink)
	app.Post("/auth/logout", auth.Logout)

	// ── Auth API (OAuth-only; no email/password) ───────────────
	app.Get("/api/auth/user/me", auth.CurrentUser)
	app.Get("/api/auth/me", auth.CurrentUser)
	app.Delete("/api/auth/account", auth.DeleteAccount)
	app.Patch("/api/auth/settings", auth.UpdateSettings)
	app.Get("/api/auth/identities", auth.ListIdentities)
	app.Delete("/api/auth/identities/:provider", auth.UnlinkIdentity)

	// ── Spendmux app APIs (session-authenticated) ──────────────
	authed := middleware.RequireAuthJSON
	app.Get("/api/models", authed, proxy.FallbackModels)
	app.Get("/api/playground/bootstrap", authed, playground.Bootstrap)

	// Bootstrap (GET, JSON) + mutations (POST form, redirect) for each page.
	// The React pages consume the GETs; the POSTs are being converted to JSON
	// one by one as the pages land.
	app.Get("/api/vault/public-key", authed, keys.VaultPublicKeyHandler)
	app.Get("/api/keys", authed, keys.KeysPageHandler)
	app.Post("/keys/provider", authed, keys.SaveProviderKeyHandler)
	app.Post("/keys/provider/:id/delete", authed, keys.DeleteProviderKeyHandler)
	app.Post("/keys/provider/:id/update", authed, keys.UpdateProviderKeyHandler)
	app.Post("/keys/platform", authed, keys.CreatePlatformKeyHandler)
	app.Post("/keys/platform/rotate", authed, keys.RotatePlatformKeyHandler)
	app.Post("/keys/platform/:id/delete", authed, keys.DeletePlatformKeyHandler)
	app.Post("/keys/platform/:id/pause", authed, keys.PausePlatformKeyHandler)
	app.Post("/keys/platform/:id/update", authed, keys.UpdatePlatformKeyHandler)
	app.Post("/keys/tenant", authed, keys.SetTenantCapHandler)
	app.Get("/api/receipt", authed, keys.ReceiptPageHandler)

	app.Get("/api/fallback", authed, keys.FallbackPageHandler)
	app.Post("/fallback", authed, keys.AddFallbackStepHandler)
	app.Post("/fallback/clear", authed, keys.ClearFallbackHandler)
	app.Post("/fallback/:index/delete", authed, keys.DeleteFallbackStepHandler)

	app.Get("/api/usage", authed, usage.PageHandler)

	// Playground stream: cookie session, origin-guarded upgrade.
	app.Use("/ws", middleware.RequireWebSocketOrigin, func(c *fiber.Ctx) error {
		if !websocket.IsWebSocketUpgrade(c) {
			return fiber.ErrUpgradeRequired
		}
		userID, err := auth.GetUserID(c)
		if err != nil || userID == uuid.Nil {
			return fiber.ErrUnauthorized
		}
		c.Locals("user_id", userID.String())
		return c.Next()
	})
	app.Get("/ws/chat", websocket.New(playground.HandleChatStream))

	if env := strings.ToLower(os.Getenv("APP_ENV")); env != "production" || os.Getenv("SPENDMUX_DEBUG") == "1" {
		app.Post("/api/debug/crash", debug.Crash)
		app.Get("/api/debug/stats", debug.Stats)
	}

	app.Get("/*", func(c *fiber.Ctx) error {
		return core.ServeStatic(c, c.Path())
	})

	return app
}

func RequireAuth() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if !auth.IsAuthenticated(c) {
			return c.Redirect("/login", fiber.StatusFound)
		}
		return c.Next()
	}
}
