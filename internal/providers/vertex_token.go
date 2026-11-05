package providers

import (
	"context"
	"sync"
	"time"

	"golang.org/x/oauth2/google"
)

var vertexTokenCache sync.Map // key: sha of SA JSON → cachedToken

type cachedToken struct {
	token     string
	expiresAt time.Time
}

// VertexAccessToken exchanges a service account JSON for a Bearer access token (cached).
func VertexAccessToken(saJSON string) (string, error) {
	key := sha256Hex([]byte(saJSON))
	if v, ok := vertexTokenCache.Load(key); ok {
		ct := v.(cachedToken)
		if time.Now().Before(ct.expiresAt.Add(-2 * time.Minute)) {
			return ct.token, nil
		}
	}

	cfg, err := google.JWTConfigFromJSON([]byte(saJSON), "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return "", err
	}
	tok, err := cfg.TokenSource(context.Background()).Token()
	if err != nil {
		return "", err
	}
	ct := cachedToken{token: tok.AccessToken, expiresAt: tok.Expiry}
	if ct.expiresAt.IsZero() {
		ct.expiresAt = time.Now().Add(50 * time.Minute)
	}
	vertexTokenCache.Store(key, ct)
	return ct.token, nil
}
