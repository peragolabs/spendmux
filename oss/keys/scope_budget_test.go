package keys_test

import (
	"errors"
	"testing"

	"github.com/vrannang1/spendmux/models"
	"github.com/vrannang1/spendmux/oss/keys"
)

func TestEnforceKeyScope(t *testing.T) {
	t.Parallel()
	any := models.PlatformAPIKey{}
	if err := keys.EnforceKeyScope(any, "groq", "m"); err != nil {
		t.Fatal(err)
	}
	prov := models.PlatformAPIKey{AllowedProvider: "groq"}
	if err := keys.EnforceKeyScope(prov, "groq", "anything"); err != nil {
		t.Fatal(err)
	}
	if err := keys.EnforceKeyScope(prov, "openai", "m"); err == nil {
		t.Fatal("expected provider deny")
	}
	both := models.PlatformAPIKey{AllowedProvider: "groq", AllowedModel: "llama-3.3-70b-versatile"}
	if err := keys.EnforceKeyScope(both, "groq", "llama-3.3-70b-versatile"); err != nil {
		t.Fatal(err)
	}
	if err := keys.EnforceKeyScope(both, "groq", "other"); err == nil {
		t.Fatal("expected model deny")
	}
}

func TestEnsureTokenBudgetUnlimited(t *testing.T) {
	t.Parallel()
	// No DB hit when limits are 0.
	if err := keys.EnsureTokenBudget(models.PlatformAPIKey{}); err != nil {
		t.Fatal(err)
	}
}

func TestReserveSpendRefusesUnknownPriceOnUSDCap(t *testing.T) {
	t.Parallel()
	capped := models.PlatformAPIKey{DailyUSDMicros: 5_000_000}
	if _, err := keys.ReserveSpend(keys.SpendRequest{Key: capped, Tokens: 10, PriceUnknown: true}); !errors.Is(err, keys.ErrPriceUnknown) {
		t.Fatalf("want ErrPriceUnknown, got %v", err)
	}
	session := models.PlatformAPIKey{SessionUSDMicros: 1_000_000}
	if _, err := keys.ReserveSpend(keys.SpendRequest{Key: session, Tokens: 10, SessionID: "run-1", PriceUnknown: true}); !errors.Is(err, keys.ErrPriceUnknown) {
		t.Fatalf("session cap: want ErrPriceUnknown, got %v", err)
	}
	// No USD cap and no tenant: nothing to protect, so the call is allowed.
	if _, err := keys.ReserveSpend(keys.SpendRequest{Key: models.PlatformAPIKey{}, Tokens: 10, PriceUnknown: true}); err != nil {
		t.Fatalf("uncapped token should pass, got %v", err)
	}
}
