package core

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vrannang1/spendmux/internal/testdb"
	"github.com/vrannang1/spendmux/models"
)

func TestOAuthStateIsSingleUseAndProviderBound(t *testing.T) {
	testdb.Setup(t)
	state := uuid.NewString()
	SetOAuthState(state, "google", OAuthStateExtra{LinkUserID: "u1"})

	if _, ok := TakeOAuthState(state, "github"); ok {
		t.Fatal("a state issued for google must not work for github")
	}
	// The wrong-provider attempt consumed it.
	if _, ok := TakeOAuthState(state, "google"); ok {
		t.Fatal("state must be single-use")
	}

	state = uuid.NewString()
	SetOAuthState(state, "google", OAuthStateExtra{LinkUserID: "u2"})
	extra, ok := TakeOAuthState(state, "google")
	if !ok || extra.LinkUserID != "u2" {
		t.Fatalf("got %+v %v", extra, ok)
	}
	if _, ok := TakeOAuthState(state, "google"); ok {
		t.Fatal("state must be single-use")
	}
}

func TestOAuthStateExpires(t *testing.T) {
	db := testdb.Setup(t)
	state := uuid.NewString()
	SetOAuthState(state, "apple", OAuthStateExtra{})
	db.Model(&models.OAuthState{}).Where("id = ?", oauthStateID(state)).Update("created_at", time.Now().Add(-10*time.Minute))
	if _, ok := TakeOAuthState(state, "apple"); ok {
		t.Fatal("an expired state must be refused")
	}
}

func TestOAuthStateStoresOnlyAHash(t *testing.T) {
	db := testdb.Setup(t)
	state := uuid.NewString()
	SetOAuthState(state, "google", OAuthStateExtra{})
	var n int64
	db.Model(&models.OAuthState{}).Where("id = ?", state).Count(&n)
	if n != 0 {
		t.Fatal("the raw state must not be stored")
	}
	TakeOAuthState(state, "google")
}
