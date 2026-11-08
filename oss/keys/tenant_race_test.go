package keys

import (
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/peragolabs/spendmux/internal/testdb"
	"github.com/peragolabs/spendmux/models"
)

// Two tokens of one account race for the last dollar of a shared tenant cap;
// only one may get it.
func TestTenantCapHoldsAcrossTokensUnderConcurrency(t *testing.T) {
	db := testdb.Setup(t)
	userID := uuid.New()
	tenant := "acme-" + uuid.NewString()[:8]
	if err := UpsertTenantCap(userID, tenant, 1_000_000); err != nil {
		t.Fatal(err)
	}
	var toks []models.PlatformAPIKey
	for i := 0; i < 8; i++ {
		k := models.PlatformAPIKey{UserID: userID, Name: "t", KeyHash: uuid.NewString(), Prefix: "sk-test"}
		if err := db.Create(&k).Error; err != nil {
			t.Fatal(err)
		}
		toks = append(toks, k)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for _, k := range toks {
		wg.Add(1)
		go func(k models.PlatformAPIKey) {
			defer wg.Done()
			// $0.60 each against a $1.00 cap: any two together overshoot.
			if _, err := ReserveSpend(SpendRequest{Key: k, Tokens: 10, USDMicros: 600_000, TenantID: tenant}); err == nil {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}(k)
	}
	wg.Wait()
	if allowed != 1 {
		t.Fatalf("%d reservations passed a cap that fits one", allowed)
	}
}
