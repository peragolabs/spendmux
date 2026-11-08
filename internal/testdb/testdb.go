// Package testdb points tests at a real Postgres. Tests that need one call
// Setup and are skipped when TEST_DATABASE_URL is unset; CI provides it.
// Tests share one migrated database, so each creates its own rows.
package testdb

import (
	"os"
	"sync"
	"testing"

	"gorm.io/gorm"

	"github.com/peragolabs/spendmux/internal/database"
)

var (
	once       sync.Once
	migrateErr error
)

// Setup returns the migrated test database or skips the test.
func Setup(t testing.TB) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	once.Do(func() {
		_ = os.Setenv("DATABASE_URL", dsn)
		// #nosec G101 -- throwaway secrets for a local test database
		for k, v := range map[string]string{
			"KEY_ENCRYPTION_SECRET": "test-key-encryption-secret-0123456789",
			"COOKIE_SECRET":         "dGVzdHRlc3R0ZXN0dGVzdHRlc3R0ZXN0dGVzdHRlc3Q=",
		} {
			if os.Getenv(k) == "" {
				_ = os.Setenv(k, v)
			}
		}
		migrateErr = database.Migrate(database.Get())
	})
	if migrateErr != nil {
		t.Fatalf("migrate test database: %v", migrateErr)
	}
	return database.Get()
}
