package core

// jwt.go — JWT helpers for Apple client-secret signing (ES256) and generic
// id_token / OIDC verification against a remote JWKS.
//
// Only github.com/golang-jwt/jwt/v5 is used for the crypto; the JWKS document
// is fetched with the stdlib http client and cached in-process by URI, then
// the matching public key (RSA or EC, selected by `kid`) is rebuilt from the
// JWK and handed to golang-jwt's verifier.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ── Apple client secret (ES256, minted fresh on every call) ───
//
// Apple's OAuth "client secret" is a short-lived signed JWT, not a static
// string. Minted per-call — signing is local and sub-millisecond, so there's
// no benefit to caching, and a cache-with-expiry scheme is a staleness trap
// for a process suspended/scaled-to-zero longer than the cache window.
func SignAppleClientSecret(teamID, keyID, clientID, privateKeyPEM string) (string, error) {
	key, err := parseECPrivateKeyPEM(privateKeyPEM)
	if err != nil {
		return "", fmt.Errorf("apple private key: %w", err)
	}
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.RegisteredClaims{
		Issuer:    teamID,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(5 * time.Minute)),
		Audience:  jwt.ClaimStrings{"https://appleid.apple.com"},
		Subject:   clientID,
	})
	token.Header["kid"] = keyID
	return token.SignedString(key)
}

// Apple ships the signing key as a PKCS#8 PEM (`-----BEGIN PRIVATE KEY-----`);
// fall back to SEC1 (`EC PRIVATE KEY`) for robustness.
func parseECPrivateKeyPEM(pemStr string) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if ec, ok := k.(*ecdsa.PrivateKey); ok {
			return ec, nil
		}
		return nil, fmt.Errorf("not an EC private key")
	}
	return x509.ParseECPrivateKey(block.Bytes)
}

// ── JWKS-backed verification ───────────────────────────────────

const jwksTTL = time.Hour

type cachedJWKS struct {
	keys      []jwkKey
	fetchedAt time.Time
}

type jwkKey struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

var (
	jwksMu    sync.Mutex
	jwksStore = make(map[string]cachedJWKS)
)

func getJWKS(jwksURI string) ([]jwkKey, error) {
	jwksMu.Lock()
	if c, ok := jwksStore[jwksURI]; ok && time.Since(c.fetchedAt) < jwksTTL {
		jwksMu.Unlock()
		return c.keys, nil
	}
	jwksMu.Unlock()

	resp, err := fetchIdentityDoc(jwksURI)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks fetch failed: %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var doc struct {
		Keys []jwkKey `json:"keys"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("jwks decode failed: %w", err)
	}

	jwksMu.Lock()
	jwksStore[jwksURI] = cachedJWKS{keys: doc.Keys, fetchedAt: time.Now()}
	jwksMu.Unlock()
	return doc.Keys, nil
}

// VerifyJWT verifies an id_token's signature, issuer, audience, and expiry
// against a remote JWKS. `algorithms` must always be passed explicitly — never
// assume the algorithm used to sign an *outbound* secret (e.g. Apple's ES256
// client secret) matches the algorithm an IdP uses to sign *inbound* id_tokens
// (Apple's are RS256).
func VerifyJWT(token, issuer, audience, jwksURI string, algorithms []string) (jwt.MapClaims, error) {
	keys, err := getJWKS(jwksURI)
	if err != nil {
		return nil, err
	}

	keyfunc := func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		for _, k := range keys {
			if k.Kid == kid {
				return publicKeyFromJWK(k)
			}
		}
		return nil, fmt.Errorf("no JWKS key for kid=%q", kid)
	}

	if len(algorithms) == 0 {
		algorithms = []string{"RS256"}
	}
	claims := jwt.MapClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods(algorithms),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(audience),
		jwt.WithLeeway(10*time.Second),
	)
	if _, err := parser.ParseWithClaims(token, claims, keyfunc); err != nil {
		return nil, err
	}
	return claims, nil
}

func publicKeyFromJWK(k jwkKey) (any, error) {
	switch k.Kty {
	case "RSA":
		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			return nil, fmt.Errorf("invalid RSA modulus: %w", err)
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			return nil, fmt.Errorf("invalid RSA exponent: %w", err)
		}
		return &rsa.PublicKey{
			N: new(big.Int).SetBytes(nBytes),
			E: int(new(big.Int).SetBytes(eBytes).Int64()),
		}, nil
	case "EC":
		var curve elliptic.Curve
		switch k.Crv {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, fmt.Errorf("unsupported EC curve %q", k.Crv)
		}
		xBytes, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return nil, fmt.Errorf("invalid EC x: %w", err)
		}
		yBytes, err := base64.RawURLEncoding.DecodeString(k.Y)
		if err != nil {
			return nil, fmt.Errorf("invalid EC y: %w", err)
		}
		return &ecdsa.PublicKey{
			Curve: curve,
			X:     new(big.Int).SetBytes(xBytes),
			Y:     new(big.Int).SetBytes(yBytes),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported key type %q", k.Kty)
	}
}

// appleUnescapePrivateKey un-escapes the literal `\n` sequences that .env files
// require (they can't hold real multi-line values) back into newlines.
func appleUnescapePrivateKey(raw string) string {
	return strings.ReplaceAll(raw, `\n`, "\n")
}

// identityHTTP fetches JWKS documents. A bounded timeout keeps a slow
// identity provider from pinning login requests open.
var identityHTTP = &http.Client{Timeout: 10 * time.Second}

func fetchIdentityDoc(rawURL string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	return identityHTTP.Do(req)
}
