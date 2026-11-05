package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// ValidateProduction fails closed when production starts with missing or weak
// security-critical configuration. Development remains intentionally lenient.
func ValidateProduction() error {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("APP_ENV")), "production") {
		return nil
	}

	required := []string{"DATABASE_URL", "BASE_URL", "COOKIE_SECRET", "KEY_ENCRYPTION_SECRET", "METRICS_TOKEN"}
	for _, name := range required {
		if strings.TrimSpace(os.Getenv(name)) == "" {
			return fmt.Errorf("%s is required in production", name)
		}
	}

	baseURL, err := url.Parse(os.Getenv("BASE_URL"))
	if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" {
		return fmt.Errorf("BASE_URL must be an absolute https URL in production")
	}
	databaseURL, err := url.Parse(os.Getenv("DATABASE_URL"))
	if err != nil || (databaseURL.Scheme != "postgres" && databaseURL.Scheme != "postgresql") || databaseURL.Host == "" {
		return fmt.Errorf("DATABASE_URL must be an absolute PostgreSQL URL")
	}
	cookieSecret := os.Getenv("COOKIE_SECRET")
	keySecret := os.Getenv("KEY_ENCRYPTION_SECRET")
	if len(cookieSecret) < 32 || len(keySecret) < 32 || len(os.Getenv("METRICS_TOKEN")) < 24 {
		return fmt.Errorf("COOKIE_SECRET and KEY_ENCRYPTION_SECRET must be at least 32 characters and METRICS_TOKEN at least 24")
	}
	if cookieSecret == keySecret {
		return fmt.Errorf("COOKIE_SECRET and KEY_ENCRYPTION_SECRET must be different")
	}
	if looksLikePlaceholder(cookieSecret) || looksLikePlaceholder(keySecret) || looksLikePlaceholder(os.Getenv("METRICS_TOKEN")) {
		return fmt.Errorf("production secrets must not use example placeholder values")
	}
	if strings.TrimSpace(os.Getenv("VAULT_PRIVATE_KEY")) == "" && strings.TrimSpace(os.Getenv("VAULT_PRIVATE_KEY_FILE")) == "" {
		return fmt.Errorf("VAULT_PRIVATE_KEY or VAULT_PRIVATE_KEY_FILE is required in production (browser-sealed provider keys)")
	}
	if os.Getenv("SPENDMUX_DEBUG") == "1" {
		return fmt.Errorf("SPENDMUX_DEBUG must be disabled in production")
	}

	githubID, githubSecret := os.Getenv("GITHUB_CLIENT_ID"), os.Getenv("GITHUB_CLIENT_SECRET")
	googleID, googleSecret := os.Getenv("GOOGLE_CLIENT_ID"), os.Getenv("GOOGLE_CLIENT_SECRET")
	if (githubID == "") != (githubSecret == "") || (googleID == "") != (googleSecret == "") {
		return fmt.Errorf("OAuth client ID and secret must be configured together")
	}
	githubReady := githubID != "" && githubSecret != ""
	googleReady := googleID != "" && googleSecret != ""
	if !githubReady && !googleReady {
		return fmt.Errorf("at least one complete OAuth provider configuration is required")
	}
	return nil
}

func looksLikePlaceholder(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "replace_with") || strings.Contains(lower, "change-me") || strings.Contains(lower, "changeme")
}
