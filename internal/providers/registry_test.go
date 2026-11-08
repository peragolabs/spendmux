package providers_test

import (
	"testing"

	"github.com/peragolabs/spendmux/internal/providers"
)

func TestResolveRoute(t *testing.T) {
	t.Parallel()
	p, m, err := providers.ResolveRoute("Groq", " openai/gpt-oss-120b ")
	if err != nil {
		t.Fatal(err)
	}
	if p != "groq" || m != "openai/gpt-oss-120b" {
		t.Fatalf("got %q %q", p, m)
	}
	if _, _, err := providers.ResolveRoute("", "m"); err == nil {
		t.Fatal("expected provider required")
	}
	if _, _, err := providers.ResolveRoute("groq", ""); err == nil {
		t.Fatal("expected model required")
	}
	if _, _, err := providers.ResolveRoute("nope", "m"); err == nil {
		t.Fatal("expected unknown provider")
	}
}

func TestGetAndGroups(t *testing.T) {
	t.Parallel()
	cfg, ok := providers.Get("openai")
	if !ok || cfg.ImagePath == "" || cfg.SpeechPath == "" {
		t.Fatalf("openai should support images/speech: %+v", cfg)
	}
	if _, ok := providers.Get("anthropic"); !ok {
		t.Fatal("missing anthropic")
	}
	anth, _ := providers.Get("anthropic")
	if anth.ModelsPath != "-" || !anth.NativeAnthropic {
		t.Fatalf("anthropic config wrong: %+v", anth)
	}
	all := providers.All()
	if len(all) < 40 {
		t.Fatalf("expected 40+ providers, got %d", len(all))
	}
	groups := providers.Groups()
	if len(groups) < 5 {
		t.Fatalf("expected category groups, got %d", len(groups))
	}
}

func TestSuggestedModels(t *testing.T) {
	t.Parallel()
	if len(providers.SuggestedModels("groq")) == 0 {
		t.Fatal("groq suggestions empty")
	}
	if len(providers.SuggestedModels("bedrock")) == 0 {
		t.Fatal("bedrock suggestions empty")
	}
	if providers.SuggestedModels("unknown-xyz") != nil {
		t.Fatal("unknown should return nil")
	}
}

func TestModalityPathsEmptyMeansUnsupported(t *testing.T) {
	t.Parallel()
	cerebras, ok := providers.Get("cerebras")
	if !ok {
		t.Fatal("missing cerebras")
	}
	if cerebras.ImagePath != "" || cerebras.SpeechPath != "" {
		t.Fatalf("cerebras should be chat-only, got image=%q speech=%q", cerebras.ImagePath, cerebras.SpeechPath)
	}
	custom, _ := providers.Get("custom")
	if custom.ImagePath == "" || custom.ModerationPath == "" {
		t.Fatal("custom should expose full modality paths")
	}
}
