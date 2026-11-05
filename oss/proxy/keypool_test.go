package proxy

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vrannang1/spendmux/models"
)

func keyRow(hint string, weight int, cooldown *time.Time) models.ProviderKey {
	k := models.ProviderKey{KeyHint: hint, Weight: weight, CooldownUntil: cooldown}
	k.ID = uuid.New()
	return k
}

func TestOrderKeysPutsCoolingKeysLastSoonestFirst(t *testing.T) {
	now := time.Now()
	soon, later, past := now.Add(time.Minute), now.Add(time.Hour), now.Add(-time.Minute)
	rows := []models.ProviderKey{
		keyRow("late", 1, &later),
		keyRow("a", 1, nil),
		keyRow("soon", 1, &soon),
		keyRow("expired-cooldown", 1, &past),
	}
	got := orderKeys(rows, now, func() float64 { return 0.5 })
	if len(got) != 4 {
		t.Fatalf("got %d candidates, want 4", len(got))
	}
	for _, c := range got[:2] {
		if c.row.KeyHint == "late" || c.row.KeyHint == "soon" {
			t.Fatalf("cooling key %q ordered before healthy keys: %v", c.row.KeyHint, hints(got))
		}
	}
	if got[2].row.KeyHint != "soon" || got[3].row.KeyHint != "late" {
		t.Fatalf("cooling keys should be soonest-ready first, got %v", hints(got))
	}
}

func TestOrderKeysFollowsWeights(t *testing.T) {
	rows := []models.ProviderKey{keyRow("heavy", 9, nil), keyRow("light", 1, nil)}
	first := map[string]int{}
	seq := []float64{}
	r := func() float64 { // deterministic pseudo-random stream
		if len(seq) == 0 {
			for i := 1; i <= 997; i++ {
				seq = append(seq, float64((i*7919)%997)/997)
			}
		}
		v := seq[0]
		seq = append(seq[1:], v)
		return v
	}
	for i := 0; i < 2000; i++ {
		first[orderKeys(rows, time.Now(), r)[0].row.KeyHint]++
	}
	share := float64(first["heavy"]) / 2000
	if share < 0.82 || share > 0.97 {
		t.Fatalf("weight 9 vs 1 should lead ~90%% of the time, got %.2f", share)
	}
}

func TestZeroWeightCountsAsOne(t *testing.T) {
	if clampWeight(0) != 1 || clampWeight(-5) != 1 || clampWeight(500) != 100 || clampWeight(7) != 7 {
		t.Fatal("clampWeight bounds wrong")
	}
}

func TestKeyRetryable(t *testing.T) {
	for _, s := range []int{0, 401, 403, 408, 429, 500, 502, 503, 529} {
		if !keyRetryable(s) {
			t.Errorf("status %d should try the next key", s)
		}
	}
	for _, s := range []int{200, 400, 404, 413, 422} {
		if keyRetryable(s) {
			t.Errorf("status %d is the request's fault; another key will not help", s)
		}
	}
}

func TestCooldownFor(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		status     int
		retryAfter string
		want       time.Duration
	}{
		{429, "", time.Minute},
		{429, "7", 7 * time.Second},
		{429, "0", time.Second},
		{429, "86400", 10 * time.Minute},
		{429, now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second},
		{503, "12", 12 * time.Second},
		{500, "12", 30 * time.Second}, // Retry-After only honoured on 429/503
		{401, "", 10 * time.Minute},
		{0, "", 30 * time.Second},
	}
	for _, c := range cases {
		if got := cooldownFor(c.status, c.retryAfter, now); got != c.want {
			t.Errorf("cooldownFor(%d, %q) = %v, want %v", c.status, c.retryAfter, got, c.want)
		}
	}
}

func TestMaxKeyAttempts(t *testing.T) {
	t.Setenv("PROVIDER_KEY_MAX_ATTEMPTS", "")
	if maxKeyAttempts(1) != 1 || maxKeyAttempts(5) != 3 {
		t.Fatal("default cap should be 3")
	}
	t.Setenv("PROVIDER_KEY_MAX_ATTEMPTS", "5")
	if maxKeyAttempts(10) != 5 {
		t.Fatal("env override ignored")
	}
}

func hints(c []keyCandidate) []string {
	out := make([]string, len(c))
	for i := range c {
		out[i] = c[i].row.KeyHint
	}
	return out
}
