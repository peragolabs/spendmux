package keys

import (
	"github.com/gofiber/fiber/v2"

	appcrypto "github.com/vrannang1/spendmux/internal/crypto"
)

// VaultPublicKeyHandler serves the public half of the vault keypair so the
// browser can seal provider keys before sending them.
func VaultPublicKeyHandler(c *fiber.Ctx) error {
	spki, kid, err := appcrypto.VaultPublicKey()
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "vault key unavailable"})
	}
	c.Set("Cache-Control", "public, max-age=300")
	return c.JSON(fiber.Map{"alg": "RSA-OAEP-256+A256GCM", "kid": kid, "spki_pem": spki})
}
