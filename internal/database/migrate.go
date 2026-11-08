package database

import (
	"github.com/peragolabs/spendmux/models"

	"gorm.io/gorm"
)

// Migrate serializes schema updates across replicas with a PostgreSQL
// transaction-scoped advisory lock. DDL rolls back if any migration fails.
func Migrate(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", "spendmux_schema_migrations").Error; err != nil {
			return err
		}
		// Provider keys were unique per (user, provider); several are allowed now.
		// AutoMigrate recreates idx_user_provider as a plain index.
		if err := tx.Exec(`DO $$ BEGIN
			IF EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_user_provider' AND indexdef LIKE 'CREATE UNIQUE%') THEN
				DROP INDEX idx_user_provider;
			END IF;
		END $$`).Error; err != nil {
			return err
		}
		if err := tx.AutoMigrate(
			// identity & shell
			&models.User{},
			&models.UserIdentity{},
			&models.Session{},
			&models.Org{},
			&models.OrgMember{},
			&models.OrgInvite{},
			// spendmux engine
			&models.ProviderKey{},
			&models.PlatformAPIKey{},
			&models.UsageEvent{},
			&models.TokenReservation{},
			&models.PlatformKeyRateLimitBucket{},
			&models.LoopFingerprintBucket{},
			&models.TenantCap{},
			&models.FallbackChain{},
			&models.MemoryThread{},
			&models.Conversation{},
			&models.Message{},
			&models.ResponseCache{},
			&models.OAuthState{},
		); err != nil {
			return err
		}
		indexes := []string{
			"CREATE INDEX IF NOT EXISTS idx_usage_events_key_created_at ON usage_events (platform_api_key_id, created_at) WHERE deleted_at IS NULL",
			"CREATE INDEX IF NOT EXISTS idx_usage_events_user_created_at ON usage_events (user_id, created_at) WHERE deleted_at IS NULL",
			"CREATE INDEX IF NOT EXISTS idx_usage_events_session ON usage_events (platform_api_key_id, session_id) WHERE deleted_at IS NULL AND session_id <> ''",
			"CREATE INDEX IF NOT EXISTS idx_usage_events_tenant ON usage_events (user_id, tenant_id) WHERE deleted_at IS NULL AND tenant_id <> ''",
			"CREATE INDEX IF NOT EXISTS idx_token_reservations_key_expires ON token_reservations (platform_api_key_id, expires_at) WHERE deleted_at IS NULL",
		}
		for _, statement := range indexes {
			if err := tx.Exec(statement).Error; err != nil {
				return err
			}
		}
		return encryptLegacyEmails(tx)
	})
}

// encryptLegacyEmails re-saves rows whose email column predates the email
// serializer (plaintext, no "enc:" prefix). Idempotent; a no-op once done.
func encryptLegacyEmails(tx *gorm.DB) error {
	var users []models.User
	if err := tx.Where("email <> '' AND email NOT LIKE 'enc:%'").Find(&users).Error; err != nil {
		return err
	}
	for i := range users {
		// Struct-based update so the email serializer runs (raw Update would not).
		if err := tx.Model(&users[i]).Select("email").Updates(models.User{Email: users[i].Email}).Error; err != nil {
			return err
		}
	}
	var invites []models.OrgInvite
	if err := tx.Unscoped().Where("email <> '' AND email NOT LIKE 'enc:%'").Find(&invites).Error; err != nil {
		return err
	}
	for i := range invites {
		if err := tx.Model(&invites[i]).Select("email").Updates(models.OrgInvite{Email: invites[i].Email}).Error; err != nil {
			return err
		}
	}
	return nil
}
