package keys

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestHashLoopRequestStable(t *testing.T) {
	t.Parallel()
	messages := []map[string]string{{"role": "user", "content": "hi"}}
	a := HashLoopRequest("gpt-4o-mini", messages)
	b := HashLoopRequest("gpt-4o-mini", messages)
	if a == "" || a != b {
		t.Fatalf("expected stable hash, got %q vs %q", a, b)
	}
	if HashLoopRequest("other", messages) == a {
		t.Fatal("model should change the fingerprint")
	}
}

func TestDollarsToMicros(t *testing.T) {
	t.Parallel()
	if DollarsToMicros("") != 0 || DollarsToMicros("nope") != -1 {
		t.Fatal("empty/invalid should be 0")
	}
	if DollarsToMicros("10") != 10_000_000 {
		t.Fatalf("got %d", DollarsToMicros("10"))
	}
	if DollarsToMicros("3.5") != 3_500_000 {
		t.Fatalf("got %d", DollarsToMicros("3.5"))
	}
}

func TestWouldExceedAndSessionWarn(t *testing.T) {
	t.Parallel()
	if wouldExceed(0, 0, 1, 0) {
		t.Fatal("limit 0 is unlimited")
	}
	if !wouldExceed(2_000_000, 500_000, 1_000_000, 3_000_000) {
		t.Fatal("expected exceed")
	}
	if wouldExceed(1_000_000, 0, 1_000_000, 3_000_000) {
		t.Fatal("should still fit")
	}
	if !sessionWouldWarn(2_400_000, 0, 0, 3_000_000) {
		t.Fatal("80% should warn")
	}
	if sessionWouldWarn(2_000_000, 0, 0, 3_000_000) {
		t.Fatal("under 80% should not warn")
	}
}

func TestLoopConfigDisable(t *testing.T) {
	t.Setenv("SPENDMUX_LOOP_MAX", "0")
	max, _ := loopConfig()
	if max != 0 {
		t.Fatalf("expected disabled loop detector, got %d", max)
	}
}

func TestMicrosToDollars(t *testing.T) {
	t.Parallel()
	if MicrosToDollars(0) != "0" {
		t.Fatal(MicrosToDollars(0))
	}
	if MicrosToDollars(3_000_000) != "3" {
		t.Fatal(MicrosToDollars(3_000_000))
	}
}

func TestSpendErrorKindsUnwrap(t *testing.T) {
	t.Parallel()
	if !errors.Is(ErrSessionBudget, ErrSessionBudget) {
		t.Fatal("session")
	}
	if !errors.Is(ErrLoopDetected, ErrLoopDetected) {
		t.Fatal("loop")
	}
	if !errors.Is(ErrTenantBudget, ErrTenantBudget) {
		t.Fatal("tenant")
	}
}

func TestInvalidDollarCapsCannotBecomeUnlimited(t *testing.T) {
	for _, input := range []string{"NaN", "+Inf", "-Inf", "1e100", "9223372036854.775808", "-1", "0.00000001"} {
		if got := DollarsToMicros(input); got != -1 {
			t.Errorf("%q produced %d, want invalid sentinel", input, got)
		}
	}
	if DollarsToMicros("0") != 0 {
		t.Fatal("explicit zero must remain supported")
	}
}

func TestNewTokenNeverGoesIntoARedirect(t *testing.T) {
	app := fiber.New()
	app.Post("/mint", func(c *fiber.Ctx) error { return finish(c, "/keys?new_key=sk-secret#tokens") })
	app.Post("/err", func(c *fiber.Ctx) error { return finish(c, "/keys?error=empty_key#tokens") })
	resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/mint", nil))
	if err != nil {
		t.Fatal(err)
	}
	if loc := resp.Header.Get("Location"); loc != "" || resp.StatusCode != 200 {
		t.Fatalf("token leaked into a redirect: %d %q", resp.StatusCode, loc)
	}
	resp, _ = app.Test(httptest.NewRequest(http.MethodPost, "/err", nil))
	if resp.StatusCode != fiber.StatusFound {
		t.Fatalf("plain form errors should still redirect: %d", resp.StatusCode)
	}
}
