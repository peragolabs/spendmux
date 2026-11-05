package models

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"

	"gorm.io/gorm/schema"
)

// Email addresses are encrypted at rest (AES-256-GCM under COOKIE_SECRET) via
// a GORM serializer, so `user.Email` reads and writes stay plain in code while
// the column holds "enc:" + base64(nonce|ciphertext). Lookups never use the
// email column — they use users.email_hash (HMAC) — so encryption costs nothing
// at query time. Rows written before this change are plaintext and are
// re-encrypted the next time the user row is saved.

const encEmailPrefix = "enc:"

func init() {
	schema.RegisterSerializer("email", emailSerializer{})
}

type emailSerializer struct{}

func emailKey() ([]byte, error) {
	secret := os.Getenv("COOKIE_SECRET")
	if secret == "" {
		return nil, errors.New("COOKIE_SECRET must be set to encrypt email addresses")
	}
	sum := sha256.Sum256([]byte("email:" + secret))
	return sum[:], nil
}

// Scan decrypts the column value into the struct field.
func (emailSerializer) Scan(ctx context.Context, field *schema.Field, dst reflect.Value, dbValue interface{}) error {
	var raw string
	switch v := dbValue.(type) {
	case nil:
		raw = ""
	case string:
		raw = v
	case []byte:
		raw = string(v)
	default:
		return fmt.Errorf("email serializer: unsupported column type %T", dbValue)
	}
	plain := raw
	if strings.HasPrefix(raw, encEmailPrefix) {
		p, err := decryptEmail(strings.TrimPrefix(raw, encEmailPrefix))
		if err != nil {
			return err
		}
		plain = p
	}
	field.ReflectValueOf(ctx, dst).SetString(plain)
	return nil
}

// Value encrypts the struct field for storage.
func (emailSerializer) Value(_ context.Context, _ *schema.Field, _ reflect.Value, fieldValue interface{}) (interface{}, error) {
	s, _ := fieldValue.(string)
	if s == "" {
		return "", nil
	}
	enc, err := encryptEmail(s)
	if err != nil {
		return nil, err
	}
	return encEmailPrefix + enc, nil
}

func encryptEmail(plain string) (string, error) {
	key, err := emailKey()
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
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plain), nil)), nil
}

func decryptEmail(encoded string) (string, error) {
	key, err := emailKey()
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", errors.New("email: bad ciphertext encoding")
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
		return "", errors.New("email: ciphertext too short")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", errors.New("email: decryption failed (COOKIE_SECRET changed?)")
	}
	return string(plain), nil
}

// DecodeStoredEmail decrypts a value read outside the ORM (raw Select/Scan).
// Plaintext (pre-encryption rows) passes through unchanged.
func DecodeStoredEmail(stored string) string {
	if !strings.HasPrefix(stored, encEmailPrefix) {
		return stored
	}
	plain, err := decryptEmail(strings.TrimPrefix(stored, encEmailPrefix))
	if err != nil {
		return ""
	}
	return plain
}

// MaskEmail renders an address for display: first character, then dots, then
// the domain ("p•••••@gmail.com"). Safe to show on pages and in logs.
func MaskEmail(email string) string {
	at := strings.Index(email, "@")
	if at <= 0 {
		return "•••"
	}
	local, domain := email[:at], email[at:]
	if len(local) <= 1 {
		return local + "•••" + domain
	}
	return local[:1] + strings.Repeat("•", min(len(local)-1, 6)) + domain
}
