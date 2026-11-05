package middleware

import (
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/time/rate"
)

// Limiter stores rate limiters for each visitor
type ipRateLimiter struct {
	ips         map[string]*rate.Limiter
	mu          sync.Mutex
	rate        rate.Limit
	burst       int
	expiry      time.Duration
	lastCleaned time.Time
}

// Create a new rate limiter
func newIPRateLimiter(r rate.Limit, b int, expiry time.Duration) *ipRateLimiter {
	return &ipRateLimiter{
		ips:         make(map[string]*rate.Limiter),
		rate:        r,
		burst:       b,
		expiry:      expiry,
		lastCleaned: time.Now(),
	}
}

// Get returns the rate limiter for the provided IP address
func (i *ipRateLimiter) getLimiter(ip string) *rate.Limiter {
	i.mu.Lock()
	defer i.mu.Unlock()

	// Clean expired entries periodically (every 5 minutes)
	now := time.Now()
	if now.Sub(i.lastCleaned) > 5*time.Minute {
		for ip, limiter := range i.ips {
			// If the limiter has tokens close to its burst capacity, it hasn't been used recently
			if limiter.Tokens() > float64(i.burst)*0.9 {
				delete(i.ips, ip)
			}
		}
		i.lastCleaned = now
	}

	limiter, exists := i.ips[ip]
	if !exists {
		limiter = rate.NewLimiter(i.rate, i.burst)
		i.ips[ip] = limiter
	}

	return limiter
}

// Create a global rate limiter that allows 30 requests per minute with a burst of 10
var limiter = newIPRateLimiter(rate.Limit(0.5), 30, 1*time.Minute) // 0.5 requests per second = 30 per minute

func RateLimitMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Production traffic is rate-limited at the trusted edge. A process-local
		// IP limiter sees the reverse proxy address and would throttle all users as
		// one client; /v1 requests still use the shared database limiter below.
		if strings.EqualFold(os.Getenv("APP_ENV"), "production") && !strings.EqualFold(os.Getenv("SPENDMUX_ENABLE_LOCAL_IP_RATE_LIMIT"), "true") {
			return c.Next()
		}
		path := c.Path()
		// /v1 is limited by authenticated platform key in PostgreSQL so all
		// production replicas share one counter.
		if strings.HasPrefix(path, "/v1/") {
			return c.Next()
		}

		// Skip rate limiting for static assets, auth, and homepage
		if strings.HasPrefix(path, "/auth") ||
			strings.HasPrefix(path, "/health/") ||
			strings.HasPrefix(path, "/static") ||
			strings.HasPrefix(path, "/public") ||
			strings.Contains(path, "/oauth") ||
			strings.Contains(path, "/callback") ||
			path == "/" || path == "/webhook" || path == "/metrics" || path == "/favicon.ico" {
			return c.Next()
		}

		ip := c.IP()
		limiter := limiter.getLimiter(ip)

		if !limiter.Allow() {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "rate_limited", "retry_after_seconds": 60})
		}

		return c.Next()
	}
}
