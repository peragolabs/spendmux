package crypto_test

import (
	"strings"
	"testing"

	appcrypto "github.com/vrannang1/spendmux/internal/crypto"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	t.Setenv("KEY_ENCRYPTION_SECRET", "unit-test-encryption-secret-please-change")
	plain := "sk-test-provider-key-abc123"
	enc, err := appcrypto.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	if enc == plain || enc == "" {
		t.Fatal("ciphertext should differ")
	}
	out, err := appcrypto.Decrypt(enc)
	if err != nil {
		t.Fatal(err)
	}
	if out != plain {
		t.Fatalf("got %q", out)
	}
}

func TestHashAndHintAndPlatformKey(t *testing.T) {
	t.Parallel()
	h1 := appcrypto.HashAPIKey("sk-abc")
	h2 := appcrypto.HashAPIKey("sk-abc")
	h3 := appcrypto.HashAPIKey("sk-xyz")
	if h1 != h2 || h1 == h3 || len(h1) != 64 {
		t.Fatalf("hash mismatch %s %s %s", h1, h2, h3)
	}
	if appcrypto.Hint("abcd") != "****" {
		t.Fatal(appcrypto.Hint("abcd"))
	}
	if !strings.HasSuffix(appcrypto.Hint("sk-abcdefgh"), "efgh") {
		t.Fatal(appcrypto.Hint("sk-abcdefgh"))
	}
	plain, err := appcrypto.NewPlatformAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plain, "sk-") || len(plain) < 20 {
		t.Fatalf("bad key %q", plain)
	}
}

func TestEncryptRequiresSecret(t *testing.T) {
	t.Setenv("KEY_ENCRYPTION_SECRET", "")
	t.Setenv("COOKIE_SECRET", "")
	_, err := appcrypto.Encrypt("x")
	if err == nil {
		t.Fatal("expected error without secret")
	}
}
