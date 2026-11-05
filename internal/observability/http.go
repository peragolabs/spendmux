package observability

import (
	"crypto/subtle"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v2"
)

var requestTotal atomic.Uint64
var requestErrors atomic.Uint64
var requestInflight atomic.Int64
var requestDurationMicros atomic.Uint64

func HTTPMetrics() fiber.Handler {
	return func(c *fiber.Ctx) error {
		requestInflight.Add(1)
		started := time.Now()
		err := c.Next()
		requestInflight.Add(-1)
		requestTotal.Add(1)
		if elapsed := time.Since(started).Microseconds(); elapsed > 0 {
			requestDurationMicros.Add(uint64(elapsed))
		}
		if err != nil || c.Response().StatusCode() >= fiber.StatusInternalServerError {
			requestErrors.Add(1)
		}
		return err
	}
}

func Metrics(c *fiber.Ctx) error {
	expected := strings.TrimSpace(os.Getenv("METRICS_TOKEN"))
	provided := strings.TrimSpace(strings.TrimPrefix(c.Get("Authorization"), "Bearer "))
	if expected == "" || len(expected) != len(provided) || subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 {
		return fiber.ErrUnauthorized
	}
	c.Set(fiber.HeaderContentType, "text/plain; version=0.0.4; charset=utf-8")
	return c.SendString(fmt.Sprintf(
		"# TYPE spendmux_http_requests_total counter\nspendmux_http_requests_total %d\n"+
			"# TYPE spendmux_http_request_errors_total counter\nspendmux_http_request_errors_total %d\n"+
			"# TYPE spendmux_http_requests_inflight gauge\nspendmux_http_requests_inflight %d\n"+
			"# TYPE spendmux_http_request_duration_seconds counter\nspendmux_http_request_duration_seconds %.6f\n",
		requestTotal.Load(), requestErrors.Load(), requestInflight.Load(), float64(requestDurationMicros.Load())/1_000_000,
	))
}
