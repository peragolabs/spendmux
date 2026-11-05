// Package memory gives a platform token opt-in, server-side conversation
// memory. A client sends X-Spendmux-Memory: <thread id> and only its new
// messages; Spendmux prepends the thread's history (trimmed to a budget),
// forwards the call to whichever provider or fallback hop serves it, then
// appends the new turn. History therefore survives a provider switch.
//
// Each thread is AES-256-GCM encrypted under a key derived from the raw
// bearer token. The server stores only the token's hash, so memory cannot be
// read at rest without the token itself; rotating or deleting the token
// makes its memory unrecoverable.
package memory

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vrannang1/spendmux/internal/database"
	"github.com/vrannang1/spendmux/models"
)

// Header names. HeaderThread opts a request in; the response echoes how many
// stored messages were sent upstream in HeaderInjected.
const (
	HeaderThread   = "X-Spendmux-Memory"
	HeaderInjected = "X-Spendmux-Memory-Messages"
)

// ErrInvalidThread is returned for thread ids outside [A-Za-z0-9._:-]{1,128}.
var ErrInvalidThread = errors.New("X-Spendmux-Memory must be 1-128 characters of letters, digits, '.', '_', ':' or '-'")

// errUndecryptable means the thread was sealed under a different token,
// i.e. the token has since been rotated.
var errUndecryptable = errors.New("memory: thread could not be decrypted with this token")

var threadPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// ValidThreadID reports whether id may name a memory thread.
func ValidThreadID(id string) bool { return threadPattern.MatchString(id) }

// Limits, overridable by env: how much history is stored per thread, how much
// is sent upstream per call, and how long an idle thread is kept.
func maxStoredMessages() int { return envInt("MEMORY_MAX_MESSAGES", 200, 2, 5000) }
func maxStoredBytes() int    { return envInt("MEMORY_MAX_BYTES", 512<<10, 4<<10, 8<<20) }
func ttl() time.Duration {
	return time.Duration(envInt("MEMORY_TTL_DAYS", 30, 1, 3650)) * 24 * time.Hour
}

// ContextBudgetChars is the most history (in JSON bytes, ~4 per token) sent
// upstream with one call; older turns stay stored but are not sent.
func ContextBudgetChars() int { return envInt("MEMORY_CONTEXT_CHARS", 48_000, 1_000, 2_000_000) }

// Role reads a raw chat message's role.
func Role(msg json.RawMessage) string {
	var m struct {
		Role string `json:"role"`
	}
	_ = json.Unmarshal(msg, &m)
	return m.Role
}

// Window returns the newest messages of history that fit in budget bytes,
// never starting on a tool result whose tool call was cut off.
func Window(history []json.RawMessage, budget int) []json.RawMessage {
	used, start := 0, len(history)
	for i := len(history) - 1; i >= 0; i-- {
		if used+len(history[i]) > budget {
			break
		}
		used += len(history[i])
		start = i
	}
	for start < len(history) && Role(history[start]) == "tool" {
		start++
	}
	return history[start:]
}

// Load returns a thread's stored messages, oldest first. A missing or expired
// thread is empty.
func Load(keyID uuid.UUID, rawToken, thread string) ([]json.RawMessage, error) {
	var row models.MemoryThread
	err := database.Get().Where("platform_api_key_id = ? AND thread_hash = ? AND expires_at > ?",
		keyID, threadHash(keyID, thread), time.Now().UTC()).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	history, err := open(row.Ciphertext, rawToken, keyID, row.ThreadHash)
	if errors.Is(err, errUndecryptable) {
		// The token was rotated: its old memory is gone by design.
		return nil, nil
	}
	return history, err
}

// Append adds messages to a thread, trimming the oldest beyond the storage
// limits, and refreshes its expiry. Concurrent appends to one thread are
// serialised by a row lock.
func Append(userID, keyID uuid.UUID, rawToken, thread string, msgs []json.RawMessage) error {
	if len(msgs) == 0 {
		return nil
	}
	hash := threadHash(keyID, thread)
	now := time.Now().UTC()
	return database.Get().Transaction(func(tx *gorm.DB) error {
		var row models.MemoryThread
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("platform_api_key_id = ? AND thread_hash = ?", keyID, hash).First(&row).Error
		var history []json.RawMessage
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			row = models.MemoryThread{UserID: userID, PlatformAPIKeyID: keyID, ThreadHash: hash}
		case err != nil:
			return err
		case row.ExpiresAt.After(now):
			history, err = open(row.Ciphertext, rawToken, keyID, hash)
			if errors.Is(err, errUndecryptable) {
				history = nil // rotated token: start the thread over
			} else if err != nil {
				return err
			}
		}
		history = trim(append(history, msgs...), maxStoredMessages(), maxStoredBytes())
		ct, err := seal(history, rawToken, keyID, hash)
		if err != nil {
			return err
		}
		row.Ciphertext, row.Messages, row.ExpiresAt = ct, len(history), now.Add(ttl())
		if row.ID == uuid.Nil {
			return tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "platform_api_key_id"}, {Name: "thread_hash"}},
				DoUpdates: clause.AssignmentColumns([]string{"ciphertext", "messages", "expires_at", "updated_at"}),
			}).Create(&row).Error
		}
		return tx.Save(&row).Error
	})
}

// Delete forgets one thread.
func Delete(keyID uuid.UUID, thread string) error {
	return database.Get().Unscoped().
		Where("platform_api_key_id = ? AND thread_hash = ?", keyID, threadHash(keyID, thread)).
		Delete(&models.MemoryThread{}).Error
}

// DeleteForKey forgets every thread of a token.
func DeleteForKey(keyID uuid.UUID) error {
	return database.Get().Unscoped().Where("platform_api_key_id = ?", keyID).Delete(&models.MemoryThread{}).Error
}

// CleanupExpired deletes threads idle past their TTL.
func CleanupExpired() {
	_ = database.Get().Unscoped().Where("expires_at <= ?", time.Now().UTC()).Delete(&models.MemoryThread{}).Error
}

func trim(history []json.RawMessage, maxMsgs, maxBytes int) []json.RawMessage {
	if len(history) > maxMsgs {
		history = history[len(history)-maxMsgs:]
	}
	total := 0
	for _, m := range history {
		total += len(m)
	}
	for len(history) > 0 && (total > maxBytes || Role(history[0]) == "tool") {
		total -= len(history[0])
		history = history[1:]
	}
	return history
}

func threadHash(keyID uuid.UUID, thread string) string {
	sum := sha256.Sum256([]byte(keyID.String() + "\x00" + thread))
	return hex.EncodeToString(sum[:])
}

// threadKey derives the AES key for one thread from the raw bearer token.
// Tokens are high-entropy random strings, so HMAC-SHA256 is a sufficient KDF.
func threadKey(rawToken string, keyID uuid.UUID, hash string) []byte {
	mac := hmac.New(sha256.New, []byte(rawToken))
	mac.Write([]byte("spendmux-memory-v1\x00" + keyID.String() + "\x00" + hash))
	return mac.Sum(nil)
}

func seal(history []json.RawMessage, rawToken string, keyID uuid.UUID, hash string) (string, error) {
	plain, err := json.Marshal(history)
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(threadKey(rawToken, keyID, hash))
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	// AAD binds the ciphertext to its row, so rows can't be swapped between threads.
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, plain, []byte(hash))), nil
}

func open(ciphertext, rawToken string, keyID uuid.UUID, hash string) ([]json.RawMessage, error) {
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(threadKey(rawToken, keyID, hash))
	if err != nil {
		return nil, err
	}
	if len(raw) < gcm.NonceSize() {
		return nil, errors.New("memory: ciphertext too short")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte(hash))
	if err != nil {
		return nil, errUndecryptable
	}
	var history []json.RawMessage
	if err := json.Unmarshal(plain, &history); err != nil {
		return nil, err
	}
	return history, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func envInt(name string, fallback, min, max int) int {
	v, err := strconv.Atoi(os.Getenv(name))
	if err != nil || v < min || v > max {
		return fallback
	}
	return v
}
