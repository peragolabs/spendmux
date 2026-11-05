package usage

import (
	"github.com/google/uuid"

	"github.com/vrannang1/spendmux/internal/database"
	"github.com/vrannang1/spendmux/models"
)

// KeepRequestHistory reports the user's per-request logging preference.
// Defaults to true (including when the user cannot be loaded) so spend
// accounting never silently loses detail because of a transient DB error.
func KeepRequestHistory(userID uuid.UUID) bool {
	var u models.User
	if err := database.Get().Select("keep_request_history").First(&u, "id = ?", userID).Error; err != nil {
		return true
	}
	return u.KeepRequestHistory
}

// ApplyHistoryPolicy strips fields that only exist for the per-request log
// when the user has turned request history off. Token counts, cost, model,
// provider, status, and session/tenant labels stay — caps depend on them.
func ApplyHistoryPolicy(ev *models.UsageEvent) {
	if ev == nil || KeepRequestHistory(ev.UserID) {
		return
	}
	ev.ErrorMessage = ""
}
