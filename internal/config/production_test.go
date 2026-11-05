package config

import "testing"

func TestValidateProductionRejectsMissingConfiguration(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "")
	if err := ValidateProduction(); err == nil {
		t.Fatal("expected missing production configuration to fail")
	}
}

func TestValidateProductionAcceptsStrongConfiguration(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://db/spendmux")
	t.Setenv("BASE_URL", "https://spendmux.example")
	t.Setenv("COOKIE_SECRET", "cookie-secret-that-is-long-and-random-123456")
	t.Setenv("KEY_ENCRYPTION_SECRET", "key-secret-that-is-separate-and-random-123")
	t.Setenv("METRICS_TOKEN", "metrics-token-long-and-random")
	t.Setenv("VAULT_PRIVATE_KEY_FILE", "/etc/spendmux/vault.pem")
	t.Setenv("GITHUB_CLIENT_ID", "client")
	t.Setenv("GITHUB_CLIENT_SECRET", "secret")
	if err := ValidateProduction(); err != nil {
		t.Fatalf("ValidateProduction returned %v", err)
	}
}
