package core

import (
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gofiber/fiber/v2"
)

// ── beam-marker template cache ────────────────────────────────

var (
	templateOnce  sync.Once
	templatePart1 string
	templatePart2 string
)

func loadTemplate() (string, string) {
	templateOnce.Do(func() {
		data, err := os.ReadFile("priv/www/index.html")
		if err != nil {
			templatePart1 = "<html><body><h1>Template Load Error</h1></body></html>"
			templatePart2 = ""
			return
		}
		// Only search before the first <script>. Next.js static export embeds
		// data-app-marker again inside RSC flight JSON; lastIndexOf there
		// corrupts the payload (Unexpected identifier 'home').
		html := string(data)
		searchEnd := len(html)
		if i := strings.Index(strings.ToLower(html), "<script"); i >= 0 {
			searchEnd = i
		}
		region := html[:searchEnd]
		i := strings.LastIndex(region, "data-app-marker")
		if i < 0 {
			i = strings.Index(html, "data-app-marker")
		}
		if i >= 0 {
			templatePart1 = html[:i]
			templatePart2 = html[i+len("data-app-marker"):]
		} else {
			templatePart1 = html
			templatePart2 = ""
		}
	})
	return templatePart1, templatePart2
}

// ServeApp injects data-app-id into the beam-marker split and serves the SPA shell
func ServeApp(c *fiber.Ctx, appID string) error {
	part1, part2 := loadTemplate()
	html := fmt.Sprintf(`%s data-app-id="%s" data-api-base="%s"%s`, part1, escapeAttr(appID), escapeAttr(PublicAPIBase(c)), part2)
	c.Set("Cache-Control", "no-store")
	c.Set("X-Content-Type-Options", "nosniff")
	return c.Type("html").Status(fiber.StatusOK).SendString(html)
}

// Serve404 serves the notfound page
func Serve404(c *fiber.Ctx) error {
	part1, part2 := loadTemplate()
	html := fmt.Sprintf(`%s data-app-id="notfound" data-api-base="%s"%s`, part1, escapeAttr(PublicAPIBase(c)), part2)
	return c.Type("html").Status(fiber.StatusNotFound).SendString(html)
}

func resolvePrivWWWPath(urlPath string) (string, error) {
	baseDir := "priv/www"
	clean := filepath.Clean(strings.TrimPrefix(urlPath, "/"))
	if clean == "." || clean == "" || filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("invalid path")
	}
	joined := filepath.Join(baseDir, clean)
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	absFull, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absBase, absFull)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("outside www root")
	}
	return absFull, nil
}

// ServeStatic serves files from priv/www safely
func ServeStatic(c *fiber.Ctx, urlPath string) error {
	fullPath, err := resolvePrivWWWPath(urlPath)
	if err != nil {
		return c.SendStatus(fiber.StatusForbidden)
	}
	data, err := os.ReadFile(fullPath) // #nosec G304 -- resolvePrivWWWPath confines it to priv/www
	if err != nil {
		return Serve404(c)
	}
	mimeType := contentType(fullPath)
	c.Set("Cache-Control", "public, max-age=31536000, immutable")
	c.Set(fiber.HeaderContentType, mimeType)
	return c.Status(fiber.StatusOK).Send(data)
}

func contentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js":
		return "application/javascript"
	case ".css":
		return "text/css"
	case ".png":
		return "image/png"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	case ".woff2":
		return "font/woff2"
	default:
		if t := mime.TypeByExtension(filepath.Ext(path)); t != "" {
			return t
		}
		return "application/octet-stream"
	}
}

func escapeAttr(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// JSON helpers
func JSON(c *fiber.Ctx, status int, data any) error {
	c.Set("Cache-Control", "no-store")
	return c.Status(status).JSON(data)
}

func IsAdminEmail(email string) bool {
	raw := os.Getenv("ADMIN_EMAILS")
	if raw == "" {
		return false
	}
	norm := strings.ToLower(strings.TrimSpace(email))
	for _, part := range strings.Split(raw, ",") {
		entry := strings.ToLower(strings.TrimSpace(part))
		if entry != "" && entry == norm {
			return true
		}
	}
	return false
}

// PublicAPIBase is the origin customers put in base_url (without /v1).
// SPENDMUX_API_URL pins it (e.g. https://api.spendmux.com) so every page shows
// the same address whichever hostname the dashboard was opened on; unset, it
// is the origin of the current request.
func PublicAPIBase(c *fiber.Ctx) string {
	if v := strings.TrimSpace(os.Getenv("SPENDMUX_API_URL")); v != "" {
		return strings.TrimSuffix(strings.TrimRight(v, "/"), "/v1")
	}
	return c.BaseURL()
}
