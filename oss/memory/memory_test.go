package memory

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func msg(role, content string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"role": role, "content": content})
	return b
}

func TestSealOpenRoundTripAndBinding(t *testing.T) {
	t.Parallel()
	key := uuid.New()
	hash := threadHash(key, "user-42")
	history := []json.RawMessage{msg("user", "my name is Asha"), msg("assistant", "Hi Asha")}
	ct, err := seal(history, "sk-spendmux-abc", key, hash)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ct, "Asha") {
		t.Fatal("ciphertext leaks plaintext")
	}
	got, err := open(ct, "sk-spendmux-abc", key, hash)
	if err != nil || len(got) != 2 || Role(got[1]) != "assistant" {
		t.Fatalf("round trip: %v %v", got, err)
	}
	if _, err := open(ct, "sk-spendmux-other", key, hash); err == nil {
		t.Fatal("a different token must not decrypt the thread")
	}
	if _, err := open(ct, "sk-spendmux-abc", key, threadHash(key, "user-43")); err == nil {
		t.Fatal("a thread's ciphertext must not open under another thread")
	}
}

func TestWindowKeepsNewestWithinBudget(t *testing.T) {
	t.Parallel()
	h := []json.RawMessage{msg("user", "one"), msg("assistant", "two"), msg("user", "three"), msg("assistant", "four")}
	budget := len(h[2]) + len(h[3])
	w := Window(h, budget)
	if len(w) != 2 || !strings.Contains(string(w[0]), "three") {
		t.Fatalf("window: %s", w)
	}
	toolFirst := []json.RawMessage{msg("tool", "result"), msg("user", "next")}
	if w := Window(toolFirst, 1<<20); len(w) != 1 || Role(w[0]) != "user" {
		t.Fatalf("window must not start on an orphan tool result: %s", w)
	}
}

func TestTrimAndThreadIDs(t *testing.T) {
	t.Parallel()
	h := []json.RawMessage{msg("user", "a"), msg("assistant", "b"), msg("user", "c")}
	if got := trim(h, 2, 1<<20); len(got) != 2 || !strings.Contains(string(got[0]), `"b"`) {
		t.Fatalf("trim by count: %s", got)
	}
	if got := trim(h, 10, len(h[2])); len(got) != 1 {
		t.Fatalf("trim by bytes: %s", got)
	}
	for _, ok := range []string{"user-42", "tenant:acme.user_7"} {
		if !ValidThreadID(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "has space", strings.Repeat("x", 129), "a/b"} {
		if ValidThreadID(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}
