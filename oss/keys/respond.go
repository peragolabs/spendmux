package keys

import (
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// finish ends a mutation. Browser form posts get the classic redirect
// (target is "/keys?error=x#tokens"-style); fetch() callers that send
// Accept: application/json get the same outcome as a JSON body instead:
//
//	{"ok":true,"new_key":"sk-…"}            or
//	{"ok":false,"error":"empty_key","message":"…"}
//
// A freshly minted token is never put in a redirect URL (browser history,
// proxy logs and Referer headers would keep it): it is always returned in a
// JSON body, whatever the Accept header.
func finish(c *fiber.Ctx, target string) error {
	if !strings.Contains(c.Get("Accept"), "application/json") && !strings.Contains(target, "new_key=") {
		return c.Redirect(target)
	}
	u, err := url.Parse(target)
	if err != nil {
		return c.JSON(fiber.Map{"ok": true})
	}
	q := u.Query()
	if code := q.Get("error"); code != "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"ok": false, "error": code, "message": flashMessage(code)})
	}
	out := fiber.Map{"ok": true}
	if k := q.Get("new_key"); k != "" {
		out["new_key"] = k
	}
	return c.JSON(out)
}

// flashMessage maps an error code to the sentence shown to the user.
func flashMessage(code string) string {
	if m := keysFlash(code); m != code {
		return m
	}
	return fallbackFlash(code)
}
