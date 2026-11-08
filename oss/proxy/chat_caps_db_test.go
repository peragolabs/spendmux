package proxy

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	appcrypto "github.com/peragolabs/spendmux/internal/crypto"
	"github.com/peragolabs/spendmux/internal/testdb"
	"github.com/peragolabs/spendmux/models"
	"gorm.io/gorm"
)

// capsFixture is one paying user with provider keys pointed at fake upstreams
// and a platform token with the given caps.
type capsFixture struct {
	db    *gorm.DB
	app   *fiber.App
	user  models.User
	key   models.PlatformAPIKey
	token string
}

func newCapsFixture(t *testing.T, key models.PlatformAPIKey, upstreams map[string]string) capsFixture {
	t.Helper()
	db := testdb.Setup(t)
	t.Setenv("SPENDMUX_ALLOW_PRIVATE_UPSTREAMS", "1")
	user := models.User{Email: uuid.NewString() + "@example.test", Role: "admin", Provider: "test", ProviderID: uuid.NewString(), Username: uuid.NewString()}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	for provider, url := range upstreams {
		enc, err := appcrypto.Encrypt("sk-test-" + provider)
		if err != nil {
			t.Fatal(err)
		}
		pk := models.ProviderKey{UserID: user.ID, Provider: provider, EncryptedKey: enc, KeyHint: "test", BaseURLOverride: url, Weight: 1}
		if err := db.Create(&pk).Error; err != nil {
			t.Fatal(err)
		}
	}
	token := "smx-test-" + uuid.NewString()
	key.UserID = user.ID
	key.Name = "caps"
	key.KeyHash = appcrypto.HashAPIKey(token)
	key.Prefix = "smx-test"
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Post("/v1/chat/completions", V1ChatCompletions)
	return capsFixture{db: db, app: app, user: user, key: key, token: token}
}

func (f capsFixture) post(t *testing.T, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+f.token)
	resp, err := f.app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func (f capsFixture) activeReservations(t *testing.T) int64 {
	t.Helper()
	var n int64
	f.db.Model(&models.TokenReservation{}).Where("platform_api_key_id = ?", f.key.ID).Count(&n)
	return n
}

// fakeUpstream records request bodies and answers with status and body.
type fakeUpstream struct {
	mu     sync.Mutex
	bodies []map[string]any
	status int
	reply  string
}

func (u *fakeUpstream) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		u.mu.Lock()
		u.bodies = append(u.bodies, m)
		u.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(u.status)
		_, _ = w.Write([]byte(u.reply))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (u *fakeUpstream) calls() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.bodies)
}

const okReply = `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`

func TestCappedReasoningRequestGetsExplicitLimitAndSettles(t *testing.T) {
	up := &fakeUpstream{status: 200, reply: okReply}
	srv := up.server(t)
	f := newCapsFixture(t, models.PlatformAPIKey{MonthlyUSDMicros: 5_000_000}, map[string]string{"openai": srv.URL})

	status, out := f.post(t, `{"provider":"openai","model":"o3","messages":[{"role":"user","content":"hi"}]}`)
	if status != 200 {
		t.Fatalf("capped o3 request refused: %d %v", status, out)
	}
	sent := up.bodies[0]
	if sent["max_completion_tokens"] != float64(32768) || sent["max_tokens"] != nil {
		t.Fatalf("o3 must carry max_completion_tokens (and never max_tokens): %v", sent)
	}
	if n := f.activeReservations(t); n != 0 {
		t.Fatalf("%d reservations left after a settled request", n)
	}
	var ev models.UsageEvent
	if err := f.db.Where("platform_api_key_id = ?", f.key.ID).First(&ev).Error; err != nil || ev.StatusCode != 200 || ev.TotalTokens != 15 {
		t.Fatalf("usage not recorded from provider usage: %+v %v", ev, err)
	}
}

func TestUncappedRequestPassesThroughUnchanged(t *testing.T) {
	up := &fakeUpstream{status: 200, reply: okReply}
	srv := up.server(t)
	f := newCapsFixture(t, models.PlatformAPIKey{}, map[string]string{"groq": srv.URL})

	status, out := f.post(t, `{"provider":"groq","model":"llama-3.3-70b-versatile","n":2,"messages":[{"role":"user","content":"hi"}]}`)
	if status != 200 {
		t.Fatalf("uncapped request refused: %d %v", status, out)
	}
	sent := up.bodies[0]
	if sent["max_tokens"] != nil || sent["max_completion_tokens"] != nil || sent["n"] != float64(2) {
		t.Fatalf("uncapped request was modified: %v", sent)
	}
}

func TestCappedRequestRefusesMultipleCompletions(t *testing.T) {
	up := &fakeUpstream{status: 200, reply: okReply}
	srv := up.server(t)
	f := newCapsFixture(t, models.PlatformAPIKey{DailyUSDMicros: 5_000_000}, map[string]string{"openai": srv.URL})
	status, _ := f.post(t, `{"provider":"openai","model":"gpt-4o-mini","n":3,"messages":[{"role":"user","content":"hi"}]}`)
	if status != 400 || up.calls() != 0 {
		t.Fatalf("n=3 on a capped key: status %d, upstream calls %d", status, up.calls())
	}
}

func TestCappedRequestOnAliasModelIsAccepted(t *testing.T) {
	up := &fakeUpstream{status: 200, reply: okReply}
	srv := up.server(t)
	f := newCapsFixture(t, models.PlatformAPIKey{MonthlyUSDMicros: 5_000_000}, map[string]string{"xai": srv.URL})
	status, out := f.post(t, `{"provider":"xai","model":"grok-4","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`)
	if status != 200 {
		t.Fatalf("grok-4 refused on a capped key: %d %v", status, out)
	}
}

func TestFallbackHopIsReservedBeforeItIsSent(t *testing.T) {
	primary := &fakeUpstream{status: 503, reply: `{"error":{"message":"overloaded"}}`}
	p := primary.server(t)
	// $0.05/day: one gpt-4o-mini attempt fits, an o1 hop at 1000 output tokens
	// ($0.06 at $60/M) does not.
	f := newCapsFixture(t, models.PlatformAPIKey{DailyUSDMicros: 50_000}, map[string]string{"openai": p.URL})
	steps, _ := json.Marshal([]models.FallbackStep{{Provider: "openai", Model: "o1"}})
	if err := f.db.Create(&models.FallbackChain{UserID: f.user.ID, StepsJSON: string(steps)}).Error; err != nil {
		t.Fatal(err)
	}
	status, out := f.post(t, `{"provider":"openai","model":"gpt-4o-mini","fallback":true,"max_tokens":1000,"messages":[{"role":"user","content":"hi"}]}`)
	if status != 429 {
		t.Fatalf("expensive fallback hop must be refused by the cap, got %d %v", status, out)
	}
	for _, b := range primary.bodies {
		if b["model"] != "gpt-4o-mini" {
			t.Fatalf("fallback hop %v was sent without a reservation covering it", b["model"])
		}
	}
	// A 503 is a refusal: nothing may be charged for it.
	var charged int64
	f.db.Model(&models.UsageEvent{}).Where("platform_api_key_id = ? AND (status_code = 499 OR status_code BETWEEN 200 AND 299)", f.key.ID).
		Select("COALESCE(SUM(cost_usd_micros),0)").Scan(&charged)
	if charged != 0 {
		t.Fatalf("refused attempt charged %d micros", charged)
	}
	if n := f.activeReservations(t); n != 0 {
		t.Fatalf("%d reservations left", n)
	}
}

func TestLostResponseIsChargedAtItsBound(t *testing.T) {
	// The provider receives the request, then the connection dies: it may
	// have generated (and billed) the reply.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 64<<10)
			_, _ = conn.Read(buf)
			_ = conn.Close()
		}
	}()
	f := newCapsFixture(t, models.PlatformAPIKey{MonthlyUSDMicros: 5_000_000}, map[string]string{"openai": "http://" + ln.Addr().String()})
	body := `{"provider":"openai","model":"gpt-4o-mini","max_tokens":1000,"messages":[{"role":"user","content":"hi"}]}`
	status, _ := f.post(t, body)
	if status != 502 {
		t.Fatalf("got %d, want 502", status)
	}
	var ev models.UsageEvent
	if err := f.db.Where("platform_api_key_id = ? AND status_code = 499", f.key.ID).First(&ev).Error; err != nil {
		t.Fatalf("lost response not charged: %v", err)
	}
	if ev.CompletionTokens != 1000 || ev.CostUSDMicros <= 0 || ev.ReservationID == nil {
		t.Fatalf("lost response must be charged at its reserved bound: %+v", ev)
	}
	if n := f.activeReservations(t); n != 0 {
		t.Fatalf("%d reservations left", n)
	}
}
