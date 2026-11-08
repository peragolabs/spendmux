package keys

import (
	"testing"
	"time"

	"github.com/peragolabs/spendmux/models"
)

func TestTokenStatus(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Minute), now.Add(time.Hour)
	cases := []struct {
		k    models.PlatformAPIKey
		want string
	}{
		{models.PlatformAPIKey{}, "active"},
		{models.PlatformAPIKey{ExpiresAt: &future}, "active"},
		{models.PlatformAPIKey{ExpiresAt: &past}, "expired"},
		{models.PlatformAPIKey{ExpiresAt: &now}, "expired"},
		{models.PlatformAPIKey{PausedAt: &past, ExpiresAt: &future}, "paused"},
	}
	for i, tc := range cases {
		if got := tokenStatus(tc.k, now); got != tc.want {
			t.Errorf("case %d: got %s, want %s", i, got, tc.want)
		}
	}
}

func TestParseExpiryForm(t *testing.T) {
	t.Parallel()
	if e, ok := parseExpiryForm(""); !ok || e != nil {
		t.Fatal("blank means no expiry")
	}
	if _, ok := parseExpiryForm("2020-01-01"); ok {
		t.Fatal("past dates are rejected")
	}
	if _, ok := parseExpiryForm("tomorrow"); ok {
		t.Fatal("non-dates are rejected")
	}
	day := time.Now().UTC().AddDate(0, 0, 7).Format("2006-01-02")
	e, ok := parseExpiryForm(day)
	if !ok || e == nil || formatDate(e) != day {
		t.Fatalf("future date: %v %v (%s)", e, ok, formatDate(e))
	}
	// The token works through the end of the chosen UTC day.
	if e.Hour() != 0 || e.Format("2006-01-02") == day {
		t.Fatalf("expiry should be the start of the next day, got %s", e)
	}
}

func TestParseWeight(t *testing.T) {
	for in, want := range map[string]int{"": 1, " 3 ": 3, "1": 1, "100": 100} {
		if got, ok := parseWeight(in); !ok || got != want {
			t.Errorf("parseWeight(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"0", "101", "-2", "1.5", "abc"} {
		if _, ok := parseWeight(in); ok {
			t.Errorf("parseWeight(%q) accepted", in)
		}
	}
}
