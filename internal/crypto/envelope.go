package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Browser-sealed provider keys.
//
// The browser generates a random AES-256-GCM data key, encrypts the provider
// key with it, wraps the data key with the vault's RSA-OAEP(SHA-256) public
// key, and sends only the envelope. The server never receives the plaintext.
// At rest the envelope is additionally encrypted with KEY_ENCRYPTION_SECRET
// (the "v2:" format handled by Encrypt/Decrypt), so recovering a key needs
// both the vault private key and the server secret. Unwrapping happens in
// memory, per request, in ResolveAPIKey.

const (
	envelopeAlg    = "RSA-OAEP-256+A256GCM"
	envelopePrefix = "v2:"
	vaultKeyBits   = 4096
)

// Envelope is what the browser sends and what is stored (server-encrypted).
type Envelope struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"` // fingerprint of the public key used to wrap
	WK  string `json:"wk"`  // base64 RSA-OAEP(wrapped AES-256 data key)
	IV  string `json:"iv"`  // base64 12-byte AES-GCM nonce
	CT  string `json:"ct"`  // base64 AES-GCM ciphertext||tag of the provider key
}

var (
	vaultOnce sync.Once
	vaultKey  *rsa.PrivateKey
	vaultErr  error
)

// VaultKey loads the vault private key once: VAULT_PRIVATE_KEY (PEM in env),
// then VAULT_PRIVATE_KEY_FILE. Outside production a missing key is generated
// at ./vault.pem so development works without setup.
func VaultKey() (*rsa.PrivateKey, error) {
	vaultOnce.Do(func() {
		if pemText := os.Getenv("VAULT_PRIVATE_KEY"); strings.TrimSpace(pemText) != "" {
			vaultKey, vaultErr = parsePrivateKey([]byte(strings.ReplaceAll(pemText, `\n`, "\n")))
			return
		}
		path := os.Getenv("VAULT_PRIVATE_KEY_FILE")
		if path == "" {
			if strings.EqualFold(os.Getenv("APP_ENV"), "production") {
				vaultErr = errors.New("VAULT_PRIVATE_KEY or VAULT_PRIVATE_KEY_FILE is required in production")
				return
			}
			path = "vault.pem"
		}
		data, err := os.ReadFile(filepath.Clean(path)) // #nosec G304 G703 -- operator-configured path
		if err == nil {
			vaultKey, vaultErr = parsePrivateKey(data)
			return
		}
		if !os.IsNotExist(err) || strings.EqualFold(os.Getenv("APP_ENV"), "production") {
			vaultErr = fmt.Errorf("read vault key %s: %w", path, err)
			return
		}
		if os.Getenv("VAULT_PRIVATE_KEY_FILE") != "" {
			// An explicit path that doesn't exist is a misconfiguration (e.g. the
			// compose secret isn't mounted) — say so instead of generating a key there.
			vaultErr = fmt.Errorf("vault key not found at VAULT_PRIVATE_KEY_FILE=%s — generate one with `openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:4096 -out vault.pem` and mount it (docker-compose: VAULT_PRIVATE_KEY_PATH)", path)
			return
		}
		vaultKey, vaultErr = generateVaultKey(path)
	})
	return vaultKey, vaultErr
}

func generateVaultKey(path string) (*rsa.PrivateKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, vaultKeyBits)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	block := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(filepath.Clean(path), block, 0o600); err != nil { // #nosec G703 -- operator-configured path
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	slog.Warn("generated development vault key — set VAULT_PRIVATE_KEY_FILE in production", "path", path)
	return key, nil
}

func parsePrivateKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("vault key: not PEM")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rk, ok := k.(*rsa.PrivateKey); ok {
			return rk, nil
		}
		return nil, errors.New("vault key: not an RSA key")
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

// VaultPublicKey returns the SPKI PEM the browser imports, plus its id.
func VaultPublicKey() (spkiPEM, kid string, err error) {
	key, err := VaultKey()
	if err != nil {
		return "", "", err
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(der)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), hex.EncodeToString(sum[:8]), nil
}

// ParseEnvelope validates the shape of a browser envelope without unwrapping it.
func ParseEnvelope(raw string) (Envelope, error) {
	var e Envelope
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		return e, errors.New("envelope: invalid JSON")
	}
	if e.Alg != envelopeAlg {
		return e, fmt.Errorf("envelope: unsupported alg %q", e.Alg)
	}
	_, kid, err := VaultPublicKey()
	if err != nil {
		return e, err
	}
	if e.Kid != kid {
		return e, errors.New("envelope: sealed with a different vault key — reload the page and try again")
	}
	for name, v := range map[string]string{"wk": e.WK, "iv": e.IV, "ct": e.CT} {
		b, err := base64.StdEncoding.DecodeString(v)
		if err != nil || len(b) == 0 {
			return e, fmt.Errorf("envelope: bad %s", name)
		}
		if name == "iv" && len(b) != 12 {
			return e, errors.New("envelope: iv must be 12 bytes")
		}
	}
	return e, nil
}

// SealEnvelope prepares a validated envelope for storage: the "v2:" prefix
// plus the server-side AES-GCM layer.
func SealEnvelope(envelopeJSON string) (string, error) {
	if _, err := ParseEnvelope(envelopeJSON); err != nil {
		return "", err
	}
	inner, err := Encrypt(envelopeJSON)
	if err != nil {
		return "", err
	}
	return envelopePrefix + inner, nil
}

// IsSealed reports whether a stored value is a browser-sealed envelope.
func IsSealed(stored string) bool { return strings.HasPrefix(stored, envelopePrefix) }

// unwrapEnvelope recovers the provider key from an envelope using the vault
// private key. The caller must not retain or log the result.
func unwrapEnvelope(envelopeJSON string) (string, error) {
	var e Envelope
	if err := json.Unmarshal([]byte(envelopeJSON), &e); err != nil {
		return "", errors.New("envelope: invalid JSON")
	}
	key, err := VaultKey()
	if err != nil {
		return "", err
	}
	wk, err := base64.StdEncoding.DecodeString(e.WK)
	if err != nil {
		return "", errors.New("envelope: bad wk")
	}
	dataKey, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, key, wk, nil)
	if err != nil {
		return "", errors.New("envelope: unwrap failed (vault key mismatch?)")
	}
	defer zero(dataKey)
	iv, err := base64.StdEncoding.DecodeString(e.IV)
	if err != nil {
		return "", errors.New("envelope: bad iv")
	}
	ct, err := base64.StdEncoding.DecodeString(e.CT)
	if err != nil {
		return "", errors.New("envelope: bad ct")
	}
	block, err := aes.NewCipher(dataKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, iv, ct, nil)
	if err != nil {
		return "", errors.New("envelope: authentication failed")
	}
	return string(plain), nil
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
