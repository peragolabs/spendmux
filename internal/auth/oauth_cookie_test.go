package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/peragolabs/spendmux/internal/core"
	"github.com/peragolabs/spendmux/internal/testdb"
	"github.com/peragolabs/spendmux/internal/utils"
	"github.com/peragolabs/spendmux/models"
)

// The browser-binding cookie must reach every registered callback path,
// including the legacy /oauth/<provider>/callback used by existing provider
// consoles and *_CALLBACK_URL overrides.
func TestOAuthStateCookieReachesLegacyCallbackPath(t *testing.T) {
	testdb.Setup(t)
	t.Setenv("GOOGLE_CLIENT_ID", "client")
	t.Setenv("GOOGLE_CALLBACK_URL", "https://spendmux.example/oauth/google/callback")
	app := fiber.New()
	app.Get("/auth/:provider", RedirectToProvider)
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/auth/google", nil), -1)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range resp.Cookies() {
		if c.Name == "spendmux_oauth_state" {
			found = true
			if c.Path != "/" || !c.HttpOnly || c.Value == "" {
				t.Fatalf("state cookie %+v does not reach /oauth/google/callback", c)
			}
		}
	}
	if !found {
		t.Fatal("no browser-binding cookie set")
	}
}

func TestEmailHashIsCaseInsensitiveAndFindsLegacyAccounts(t *testing.T) {
	db := testdb.Setup(t)
	if utils.HashEmail(" Alice@Example.COM ") != utils.HashEmail("alice@example.com") {
		t.Fatal("email hash must ignore case and surrounding space (invites hash the lower-cased form)")
	}
	id := uuid.NewString()
	mixed := "Mixed." + id + "@Example.com"
	legacy := models.User{Username: id, ProviderID: id, Email: strings.ToLower(mixed), EmailHash: utils.LegacyHashEmail(mixed)}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	got, _, err := FindOrCreateUser(core.OAuthUserInfo{OAuthID: "gh-" + id, Email: mixed, EmailVerified: true, Name: "M"}, "github", "")
	if err != nil || got != legacy.ID {
		t.Fatalf("verified sign-in did not find the legacy-hashed account: %v %v", got, err)
	}
	var after models.User
	db.First(&after, "id = ?", legacy.ID)
	if after.EmailHash != utils.HashEmail(mixed) {
		t.Fatal("legacy hash not upgraded on sign-in")
	}
}
