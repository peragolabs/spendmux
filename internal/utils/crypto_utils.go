package utils

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

func getKey() []byte {
	secret := os.Getenv("COOKIE_SECRET")
	if secret == "" {
		panic("COOKIE_SECRET must be set")
	}
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// EncryptEmail encrypts an email address with AES-GCM
func EncryptEmail(email string) ([]byte, error) {
	block, err := aes.NewCipher(getKey())
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, []byte(email), nil), nil
}

// DecryptEmail decrypts an encrypted email
func DecryptEmail(data []byte) (string, error) {
	block, err := aes.NewCipher(getKey())
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// HashEmail returns a hex SHA-256 hash for lookup without decryption. The
// address is trimmed and lower-cased first: invites always hashed the
// lower-cased form while sign-in hashed the provider's casing, so an invite
// to Alice@x.com never matched the account.
func HashEmail(email string) string {
	return LegacyHashEmail(strings.ToLower(strings.TrimSpace(email)))
}

// LegacyHashEmail is the pre-normalization hash, still stored on accounts
// that have not signed in since; lookups match it too until then.
func LegacyHashEmail(email string) string {
	h := sha256.Sum256([]byte(email))
	return hex.EncodeToString(h[:])
}

// GenerateID returns a UUID-style random hex string
func GenerateID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// HashToken returns a SHA-256 hex digest for reset tokens.
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// EncryptSecret encrypts an arbitrary secret at rest (e.g. an OIDC client
// secret). Same AES-256-GCM scheme as EncryptEmail — the wrappers are named
// per use-site for clarity, not because the algorithm differs.
func EncryptSecret(plaintext string) ([]byte, error) {
	return EncryptEmail(plaintext)
}

// DecryptSecret reverses EncryptSecret, returning "" on any failure so callers
// can treat a corrupt/absent secret as simply unusable.
func DecryptSecret(data []byte) string {
	s, err := DecryptEmail(data)
	if err != nil {
		return ""
	}
	return s
}
