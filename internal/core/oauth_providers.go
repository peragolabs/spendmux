package core

// oauth_providers.go — data-driven registry of *static*, env-var-configured
// OAuth providers (Google, GitHub, Apple). New static providers go
// here, NOT as if/else branches in internal/auth.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// OAuthUserInfo is the normalized identity every provider resolves to.
type OAuthUserInfo struct {
	OAuthID       string
	Email         string
	EmailVerified bool
	Name          string
}

// OAuthProvider is a single static provider's config. Secret/URL getters are
// funcs so env vars are read lazily (and Apple's secret is minted per-call).
type OAuthProvider struct {
	Label                string
	Scope                string
	ExtraAuthorizeParams map[string]string
	ExtraTokenParams     map[string]string
	AuthorizeURL         func() string
	TokenURL             func() string
	ClientID             func() string
	ClientSecret         func() (string, error)
	RedirectURI          func() string
	// FetchUserInfo resolves identity from the token response. rawBody carries
	// the callback source (query or POST form) — Apple reads its one-time
	// `user` param from there.
	FetchUserInfo func(tokenResp map[string]any, rawBody url.Values) (*OAuthUserInfo, error)
}

// oauthEnv returns the first non-empty env var among keys.
func oauthEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// callbackURL returns the explicit env override or BASE_URL + /auth/<provider>/callback.
func callbackURL(provider string, keys ...string) string {
	if v := oauthEnv(keys...); v != "" {
		return v
	}
	base := strings.TrimRight(os.Getenv("BASE_URL"), "/")
	if base == "" {
		base = "http://localhost:8080"
	}
	return base + "/auth/" + provider + "/callback"
}

var providers = map[string]*OAuthProvider{
	"google": {
		Label:                "Google",
		Scope:                "email profile",
		ExtraAuthorizeParams: map[string]string{},
		ExtraTokenParams:     map[string]string{"grant_type": "authorization_code"},
		AuthorizeURL:         func() string { return "https://accounts.google.com/o/oauth2/auth" },
		TokenURL:             func() string { return "https://oauth2.googleapis.com/token" },
		ClientID:             func() string { return oauthEnv("GOOGLE_CLIENT_ID", "GOOGLE_OAUTH_CLIENT_ID") },
		ClientSecret: func() (string, error) {
			return oauthEnv("GOOGLE_CLIENT_SECRET", "GOOGLE_OAUTH_SECRET", "GOOGLE_OAUTH_CLIENT_SECRET"), nil
		},
		RedirectURI: func() string {
			return callbackURL("google", "GOOGLE_CALLBACK_URL", "GOOGLE_REDIRECT_URI", "GOOGLE_OAUTH_REDIRECT_URI")
		},
		FetchUserInfo: func(tokenResp map[string]any, _ url.Values) (*OAuthUserInfo, error) {
			data, err := getJSON("https://www.googleapis.com/oauth2/v2/userinfo", map[string]string{
				"Authorization": "Bearer " + str(tokenResp["access_token"]),
			})
			if err != nil {
				return nil, err
			}
			return &OAuthUserInfo{
				OAuthID:       str(data["id"]),
				Email:         str(data["email"]),
				EmailVerified: data["verified_email"] == true,
				Name:          str(data["name"]),
			}, nil
		},
	},

	"github": {
		Label:                "GitHub",
		Scope:                "user:email",
		ExtraAuthorizeParams: map[string]string{},
		ExtraTokenParams:     map[string]string{},
		AuthorizeURL:         func() string { return "https://github.com/login/oauth/authorize" },
		TokenURL:             func() string { return "https://github.com/login/oauth/access_token" },
		ClientID:             func() string { return oauthEnv("GITHUB_CLIENT_ID", "GITHUB_OAUTH_CLIENT_ID") },
		ClientSecret: func() (string, error) {
			return oauthEnv("GITHUB_CLIENT_SECRET", "GITHUB_OAUTH_SECRET", "GITHUB_OAUTH_CLIENT_SECRET"), nil
		},
		RedirectURI: func() string {
			return callbackURL("github", "GITHUB_CALLBACK_URL", "GITHUB_REDIRECT_URI", "GITHUB_OAUTH_REDIRECT_URI")
		},
		FetchUserInfo: func(tokenResp map[string]any, _ url.Values) (*OAuthUserInfo, error) {
			headers := map[string]string{
				"Authorization": "token " + str(tokenResp["access_token"]),
				"User-Agent":    "keller-chi-app",
				"Accept":        "application/json",
			}
			user, err := getJSON("https://api.github.com/user", headers)
			if err != nil {
				return nil, err
			}

			email := ""
			emailVerified := false
			if emails, err := getJSONArray("https://api.github.com/user/emails", headers); err == nil {
				var primary map[string]any
				for _, e := range emails {
					if e["primary"] == true {
						primary = e
						break
					}
				}
				if primary == nil {
					for _, e := range emails {
						if e["verified"] == true {
							primary = e
							break
						}
					}
				}
				if primary != nil {
					email = str(primary["email"])
					emailVerified = primary["verified"] == true
				}
			}
			if email == "" {
				if e := str(user["email"]); e != "" {
					email = e
				} else {
					email = fmt.Sprintf("github_%s@placeholder.local", str(user["id"]))
				}
				emailVerified = false
			}

			return &OAuthUserInfo{
				OAuthID:       str(user["id"]),
				Email:         email,
				EmailVerified: emailVerified,
				Name:          str(user["login"]),
			}, nil
		},
	},

	"apple": {
		Label: "Apple",
		Scope: "name email",
		// response_mode=form_post is required by Apple whenever name/email
		// scopes are requested — this is also why the callback route accepts POST.
		ExtraAuthorizeParams: map[string]string{"response_mode": "form_post"},
		ExtraTokenParams:     map[string]string{"grant_type": "authorization_code"},
		AuthorizeURL:         func() string { return "https://appleid.apple.com/auth/authorize" },
		TokenURL:             func() string { return "https://appleid.apple.com/auth/token" },
		ClientID:             func() string { return oauthEnv("APPLE_CLIENT_ID") },
		// Apple's "client secret" is a short-lived signed JWT, not a static string.
		ClientSecret: func() (string, error) {
			return SignAppleClientSecret(
				oauthEnv("APPLE_TEAM_ID"),
				oauthEnv("APPLE_KEY_ID"),
				oauthEnv("APPLE_CLIENT_ID"),
				appleUnescapePrivateKey(oauthEnv("APPLE_PRIVATE_KEY")),
			)
		},
		RedirectURI: func() string { return callbackURL("apple", "APPLE_CALLBACK_URL") },
		// Apple's identity comes from verifying the id_token JWT, not a REST
		// userinfo call. rawBody carries the one-time `user` JSON param, present
		// ONLY on the very first authorization ever.
		FetchUserInfo: func(tokenResp map[string]any, rawBody url.Values) (*OAuthUserInfo, error) {
			idToken := str(tokenResp["id_token"])
			if idToken == "" {
				return nil, fmt.Errorf("no id_token in apple token response")
			}
			// Apple's id_token is RS256 — the ES256 key is only for the
			// outbound client secret; never assume inbound/outbound algs match.
			payload, err := VerifyJWT(idToken, "https://appleid.apple.com", oauthEnv("APPLE_CLIENT_ID"),
				"https://appleid.apple.com/auth/keys", []string{"RS256"})
			if err != nil {
				return nil, err
			}

			name := ""
			if rawUser := rawBody.Get("user"); rawUser != "" {
				var parsed struct {
					Name struct {
						FirstName string `json:"firstName"`
						LastName  string `json:"lastName"`
					} `json:"name"`
				}
				if json.Unmarshal([]byte(rawUser), &parsed) == nil {
					name = strings.TrimSpace(parsed.Name.FirstName + " " + parsed.Name.LastName)
				}
			}

			emailVerified := payload["email_verified"] == true || payload["email_verified"] == "true"
			return &OAuthUserInfo{
				OAuthID:       str(payload["sub"]),
				Email:         str(payload["email"]),
				EmailVerified: emailVerified,
				Name:          name,
			}, nil
		},
	},
}

// ConfiguredProviders lists the sign-in providers that have a client id set,
// in the order the login page shows them.
func ConfiguredProviders() []string {
	out := []string{}
	for _, id := range []string{"google", "github", "apple"} {
		if p := providers[id]; p != nil && p.ClientID() != "" {
			out = append(out, id)
		}
	}
	return out
}

// GetProvider returns the static provider config for id, or nil.
func GetProvider(id string) *OAuthProvider {
	return providers[id]
}

// ── small HTTP/JSON helpers used by the registry ──────────────

func str(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		// JSON numbers decode to float64; render integer-valued ids cleanly.
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

func getJSON(u string, headers map[string]string) (map[string]any, error) {
	body, err := doGet(u, headers)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("decode failed: %w", err)
	}
	return data, nil
}

func getJSONArray(u string, headers map[string]string) ([]map[string]any, error) {
	body, err := doGet(u, headers)
	if err != nil {
		return nil, err
	}
	var data []map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("decode failed: %w", err)
	}
	return data, nil
}

func doGet(u string, headers map[string]string) ([]byte, error) {
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// ExchangeOAuthCode posts the authorization code to the provider's token
// endpoint and returns the full parsed token response (access_token for
// REST-userinfo providers, id_token for Apple).
func ExchangeOAuthCode(p *OAuthProvider, code string) (map[string]any, error) {
	secret, err := p.ClientSecret()
	if err != nil {
		return nil, err
	}
	form := url.Values{
		"code":          {code},
		"client_id":     {p.ClientID()},
		"client_secret": {secret},
		"redirect_uri":  {p.RedirectURI()},
	}
	for k, v := range p.ExtraTokenParams {
		form.Set(k, v)
	}
	req, _ := http.NewRequest(http.MethodPost, p.TokenURL(), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange HTTP %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("token decode failed: %w", err)
	}
	return data, nil
}
