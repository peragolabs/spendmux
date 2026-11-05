package auth

import (
	"testing"

	"github.com/google/uuid"

	"github.com/vrannang1/spendmux/internal/core"
	"github.com/vrannang1/spendmux/internal/testdb"
	"github.com/vrannang1/spendmux/models"
)

func roleOf(t *testing.T, id uuid.UUID) string {
	t.Helper()
	var u models.User
	if err := testdb.Setup(t).First(&u, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	return u.Role
}

func TestAdminRequiresVerifiedEmail(t *testing.T) {
	testdb.Setup(t)
	admin := "boss-" + uuid.NewString()[:8] + "@example.com"
	t.Setenv("ADMIN_EMAILS", admin)

	// A company OIDC connection (never verified) asserting the admin address.
	id, _, err := FindOrCreateUser(core.OAuthUserInfo{OAuthID: uuid.NewString(), Email: admin, Name: "x"}, "oidc:company", "")
	if err != nil {
		t.Fatal(err)
	}
	if r := roleOf(t, id); r == "admin" {
		t.Fatal("an unverified email must not grant admin")
	}

	// The same address verified by the provider does.
	id, _, err = FindOrCreateUser(core.OAuthUserInfo{OAuthID: uuid.NewString(), Email: admin, EmailVerified: true, Name: "x"}, "google", "")
	if err != nil {
		t.Fatal(err)
	}
	if r := roleOf(t, id); r != "admin" {
		t.Fatalf("verified admin email got role %q", r)
	}
}

func TestUnverifiedSignInCannotClaimAnEmail(t *testing.T) {
	testdb.Setup(t)
	email := "victim-" + uuid.NewString()[:8] + "@example.com"
	squatter, _, err := FindOrCreateUser(core.OAuthUserInfo{OAuthID: uuid.NewString(), Email: email}, "oidc:company", "")
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := FindOrCreateUser(core.OAuthUserInfo{OAuthID: uuid.NewString(), Email: email, EmailVerified: true}, "google", "")
	if err != nil {
		t.Fatal(err)
	}
	if owner == squatter {
		t.Fatal("a verified login must not be merged into an account created from an unverified email")
	}
	again, _, _ := FindOrCreateUser(core.OAuthUserInfo{OAuthID: uuid.NewString(), Email: email, EmailVerified: true}, "github", "")
	if again != owner {
		t.Fatal("verified logins with the same email should still find the same account")
	}
}
