package maintenance

import (
	"log/slog"
	"time"

	"github.com/peragolabs/spendmux/internal/supervision"
	"github.com/peragolabs/spendmux/oss/keys"
	"github.com/peragolabs/spendmux/oss/proxy"
)

// Start runs small, idempotent database cleanup jobs. It is safe for every
// replica to run this loop.
func Start() func() {
	stops := []func(){
		supervision.Every("reservation-cleanup", 5*time.Minute, false, func() {
			if err := keys.CleanupExpiredTokenReservations(); err != nil {
				slog.Error("reservation_cleanup_failed", "error", err)
			}
		}),
		supervision.Every("spend-state-purge", 10*time.Minute, false, func() {
			if _, err := keys.PurgeStaleSpendState(); err != nil {
				slog.Error("spend_state_purge_failed", "error", err)
			}
		}),
		supervision.Every("response-cache-purge", 10*time.Minute, false, func() {
			if err := proxy.PurgeExpiredCache(); err != nil {
				slog.Error("response_cache_purge_failed", "error", err)
			}
		}),
	}
	return func() {
		for _, stop := range stops {
			stop()
		}
	}
}
