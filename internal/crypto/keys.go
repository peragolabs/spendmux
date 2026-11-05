package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
)

func encryptionKey() ([]byte, error) {
	secret := os.Getenv("KEY_ENCRYPTION_SECRET")
	if secret == "" && !strings.EqualFold(os.Getenv("APP_ENV"), "production") {
		secret = os.Getenv("COOKIE_SECRET")
	}
	if secret == "" {
		return nil, errors.New("KEY_ENCRYPTION_SECRET required")
	}
	sum := sha256.Sum256([]byte(secret))
	return sum[:], nil
}

// CookieKey returns a Fiber encryptcookie Key: base64(32-byte AES key).
// COOKIE_SECRET may be any string, or already a base64 16/24/32-byte key
// (e.g. from encryptcookie.GenerateKey / openssl rand -base64 32).
func CookieKey() (string, error) {
	secret := os.Getenv("COOKIE_SECRET")
	if secret == "" {
		return "", errors.New("COOKIE_SECRET required")
	}
	if decoded, err := base64.StdEncoding.DecodeString(secret); err == nil {
		switch len(decoded) {
		case 16, 24, 32:
			return secret, nil
		}
	}
	sum := sha256.Sum256([]byte(secret))
	return base64.StdEncoding.EncodeToString(sum[:]), nil
}

// Encrypt returns base64(nonce|ciphertext).
func Encrypt(plaintext string) (string, error) {
	key, err := encryptionKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	out := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

// Decrypt recovers a vault value. "v2:" values are browser-sealed envelopes:
// peel the server layer, then unwrap with the vault private key. Older
// values are plain server-side AES-GCM.
func Decrypt(encoded string) (string, error) {
	if strings.HasPrefix(encoded, envelopePrefix) {
		envelopeJSON, err := decryptServerLayer(strings.TrimPrefix(encoded, envelopePrefix))
		if err != nil {
			return "", err
		}
		return unwrapEnvelope(envelopeJSON)
	}
	return decryptServerLayer(encoded)
}

func decryptServerLayer(encoded string) (string, error) {
	key, err := encryptionKey()
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func HashAPIKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func Hint(raw string) string {
	if len(raw) <= 4 {
		return "****"
	}
	return "…" + raw[len(raw)-4:]
}

func NewPlatformAPIKey() (plain string, err error) {
	b := make([]byte, 24)
	if _, err = rand.Read(b); err != nil {
		return "", err
	}
	return "sk-" + hex.EncodeToString(b), nil
}

// Derive returns HMAC-SHA256(server secret, label|data). It keys values that
// must be unreadable without both the server secret and the original input,
// such as cached responses keyed by the request that produced them.
func Derive(label string, data ...[]byte) [32]byte {
	secret, err := encryptionKey()
	if err != nil {
		secret = []byte("spendmux-dev-derive")
	}
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(label))
	for _, d := range data {
		m.Write([]byte{0})
		m.Write(d)
	}
	var out [32]byte
	copy(out[:], m.Sum(nil))
	return out
}

// SealWith encrypts plaintext under key with AES-256-GCM: nonce|ciphertext.
func SealWith(key [32]byte, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key[:])
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
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// OpenWith reverses SealWith.
func OpenWith(key [32]byte, sealed []byte) ([]byte, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	return gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], nil)
}
