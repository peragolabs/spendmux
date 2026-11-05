package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"sync"
	"testing"
)

// sealLikeBrowser does what KeysPage.tsx does with WebCrypto.
func sealLikeBrowser(t *testing.T, spkiPEM, kid, secret string) string {
	t.Helper()
	block, _ := pem.Decode([]byte(spkiPEM))
	pubAny, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pub := pubAny.(*rsa.PublicKey)
	dataKey := make([]byte, 32)
	iv := make([]byte, 12)
	_, _ = rand.Read(dataKey)
	_, _ = rand.Read(iv)
	blk, _ := aes.NewCipher(dataKey)
	gcm, _ := cipher.NewGCM(blk)
	ct := gcm.Seal(nil, iv, []byte(secret), nil)
	wk, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, dataKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(Envelope{Alg: envelopeAlg, Kid: kid, WK: base64.StdEncoding.EncodeToString(wk), IV: base64.StdEncoding.EncodeToString(iv), CT: base64.StdEncoding.EncodeToString(ct)})
	return string(b)
}

func TestBrowserEnvelopeRoundTrip(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("KEY_ENCRYPTION_SECRET", "unit-test-secret-that-is-long-enough-32")
	t.Setenv("VAULT_PRIVATE_KEY_FILE", "") // default dev path (./vault.pem) is auto-generated…
	t.Chdir(t.TempDir())                   // …inside a scratch working directory
	vaultOnce = sync.Once{}

	spki, kid, err := VaultPublicKey()
	if err != nil {
		t.Fatal(err)
	}
	env := sealLikeBrowser(t, spki, kid, "sk-provider-secret-123")

	stored, err := SealEnvelope(env)
	if err != nil {
		t.Fatal(err)
	}
	if !IsSealed(stored) {
		t.Fatal("expected v2 prefix")
	}
	if stored == env || contains(stored, "sk-provider") {
		t.Fatal("stored value must not contain the envelope or plaintext")
	}
	got, err := Decrypt(stored)
	if err != nil {
		t.Fatal(err)
	}
	if got != "sk-provider-secret-123" {
		t.Fatalf("got %q", got)
	}

	// Wrong kid is rejected before storage.
	if _, err := SealEnvelope(sealLikeBrowser(t, spki, "deadbeef", "x")); err == nil {
		t.Fatal("expected kid mismatch error")
	}
	// Legacy server-only values still decrypt.
	legacy, _ := Encrypt("old-format")
	if v, _ := Decrypt(legacy); v != "old-format" {
		t.Fatal("legacy format broken")
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
