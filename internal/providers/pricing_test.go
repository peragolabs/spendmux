package providers_test

import (
	"testing"

	"github.com/peragolabs/spendmux/internal/providers"
)

func TestEstimateCostUSDMicros(t *testing.T) {
	t.Parallel()
	c := providers.EstimateCostUSDMicros("openai", "gpt-4o-mini", 1_000_000, 1_000_000)
	// 0.15 in + 0.60 out = 0.75 USD = 750_000 micros
	if c != 750_000 {
		t.Fatalf("got %d", c)
	}
	unknown := providers.EstimateCostUSDMicros("totally-unknown", "nope", 100, 100)
	if unknown != 0 {
		t.Fatalf("unknown should be 0, got %d", unknown)
	}
	def := providers.EstimateCostUSDMicros("groq", "some-random-model", 1_000_000, 0)
	if def <= 0 {
		t.Fatalf("provider default should apply, got %d", def)
	}
}

// The old prefix table was a Go map, so a dated gpt-4o-mini could be priced
// as gpt-4o on some runs. Lookups must be deterministic.
func TestLookupIsDeterministic(t *testing.T) {
	t.Parallel()
	want := providers.EstimateCostUSDMicros("openai", "gpt-4o-mini", 1_000_000, 0)
	for i := 0; i < 200; i++ {
		if got := providers.EstimateCostUSDMicros("openai", "gpt-4o-mini-2024-07-18", 1_000_000, 0); got != want {
			t.Fatalf("run %d: dated gpt-4o-mini priced %d, want %d", i, got, want)
		}
	}
}

func TestTableCoversFlagshipModels(t *testing.T) {
	t.Parallel()
	cases := []struct {
		provider, model string
		minInPerM       float64 // list input price must be at least this
	}{
		{"openai", "o1", 10},
		{"anthropic", "claude-3-opus-20240229", 10},
		{"google", "gemini-2.5-pro", 1},
		{"groq", "openai/gpt-oss-120b", 0.1},
		{"cerebras", "gpt-oss-120b", 0.1},
		{"openrouter", "anthropic/claude-3.5-sonnet", 2},
		{"bedrock", "anthropic.claude-3-5-sonnet-20240620-v1:0", 2},
	}
	for _, tc := range cases {
		p, ok := providers.LookupPrice(tc.provider, tc.model)
		if !ok {
			t.Errorf("%s/%s: no price", tc.provider, tc.model)
			continue
		}
		if p.In < tc.minInPerM {
			t.Errorf("%s/%s: input $%.2f/M from %s, want >= $%.2f", tc.provider, tc.model, p.In, p.Source, tc.minInPerM)
		}
	}
}

func TestUnknownAndLocal(t *testing.T) {
	t.Parallel()
	if providers.PriceKnown("custom", "my-finetune") {
		t.Fatal("custom provider models must be unknown, not free")
	}
	if p, ok := providers.LookupPrice("ollama", "llama3"); !ok || p.In != 0 || p.Source != "local" {
		t.Fatalf("ollama should be known and free, got %+v %v", p, ok)
	}
}

func TestModalityPricing(t *testing.T) {
	t.Parallel()
	img, ok := providers.EstimateImageUSDMicros("openai", "dall-e-3", 2, "1024x1024", "hd")
	if !ok || img != 160_000 { // 2 × $0.08
		t.Fatalf("dall-e-3 hd ×2 = %d (%v), want 160000", img, ok)
	}
	if noQ, _ := providers.EstimateImageUSDMicros("openai", "dall-e-3", 1, "1024x1024", ""); noQ < 80_000 {
		t.Fatalf("unspecified quality should price at the top tier, got %d", noQ)
	}
	tts, ok := providers.EstimateSpeechUSDMicros("openai", "tts-1", 1000)
	if !ok || tts != 15_000 { // 1000 chars × $15/M
		t.Fatalf("tts-1 1000 chars = %d (%v)", tts, ok)
	}
	stt, ok := providers.EstimateTranscriptionUSDMicros("openai", "whisper-1", 60)
	if !ok || stt != 6_000 { // 60 s × $0.0001
		t.Fatalf("whisper-1 60s = %d (%v)", stt, ok)
	}
	if _, ok := providers.EstimateImageUSDMicros("custom", "sdxl", 1, "", ""); ok {
		t.Fatal("unknown image model should be unknown")
	}
}

func TestEstimateCachedCost(t *testing.T) {
	full := providers.EstimateCostUSDMicros("anthropic", "claude-sonnet-4-5", 10000, 100)
	read := providers.EstimateCachedCostUSDMicros("anthropic", "claude-sonnet-4-5", 10000, 100, 9000, 0)
	write := providers.EstimateCachedCostUSDMicros("anthropic", "claude-sonnet-4-5", 10000, 100, 0, 9000)
	if !(read < full && full < write) {
		t.Fatalf("read %d full %d write %d", read, full, write)
	}
	// 1000 input at $3 + 9000 read at $0.30 + 100 out at $15 per 1M = 0.003 + 0.0027 + 0.0015
	if read != 7200 {
		t.Fatalf("read cost = %d micros, want 7200", read)
	}
	if got := providers.EstimateCachedCostUSDMicros("anthropic", "claude-sonnet-4-5", 10000, 100, 0, 0); got != full {
		t.Fatalf("no cache tokens must equal the plain estimate: %d vs %d", got, full)
	}
}

func TestGuessedPricesCannotAuthorizeDollarCaps(t *testing.T) {
	for _, tc := range [][2]string{{"groq", "some-random-model"}, {"custom", "gpt-4o-new-unpriced-model"}} {
		if providers.PriceKnown(tc[0], tc[1]) {
			t.Fatalf("guessed price for %v accepted as cap-safe", tc)
		}
	}
	if !providers.PriceKnown("openai", "gpt-4o-mini") {
		t.Fatal("explicit table price must remain supported")
	}
	reservation := providers.EstimateReservationUSDMicros("anthropic", "claude-sonnet-4-5", 10000, 100)
	write := providers.EstimateCachedCostUSDMicros("anthropic", "claude-sonnet-4-5", 10000, 100, 0, 10000)
	if reservation < write {
		t.Fatalf("reservation %d does not cover cache-write cost %d", reservation, write)
	}
}

// Common models and aliases must stay usable on USD-capped keys. Several
// (Claude 4/4.1, 3.x "-latest", Grok 4, Gemini 2.0, Groq Llama) are no longer
// in the generated table and were refused once guesses stopped counting.
func TestCommonModelsAndAliasesAreCapSafe(t *testing.T) {
	for _, tc := range [][2]string{
		{"openai", "gpt-4o"}, {"openai", "gpt-4o-mini"}, {"openai", "gpt-4o-2024-08-06"}, {"openai", "chatgpt-4o-latest"},
		{"openai", "gpt-4.1"}, {"openai", "gpt-4.1-mini"}, {"openai", "gpt-5"}, {"openai", "gpt-5-mini"}, {"openai", "gpt-5-nano"},
		{"openai", "o1"}, {"openai", "o3"}, {"openai", "o3-mini"}, {"openai", "o4-mini"}, {"openai", "gpt-3.5-turbo"},
		{"anthropic", "claude-sonnet-4-5"}, {"anthropic", "claude-sonnet-4-5-20250929"}, {"anthropic", "claude-haiku-4-5"},
		{"anthropic", "claude-opus-4-1"}, {"anthropic", "claude-opus-4-1-20250805"}, {"anthropic", "claude-opus-4-20250514"},
		{"anthropic", "claude-sonnet-4-20250514"}, {"anthropic", "claude-sonnet-4-0"}, {"anthropic", "claude-3-7-sonnet-latest"},
		{"anthropic", "claude-3-5-sonnet-latest"}, {"anthropic", "claude-3-5-haiku-latest"}, {"anthropic", "claude-3-haiku-20240307"},
		{"google", "gemini-2.5-pro"}, {"google", "gemini-2.5-flash"}, {"google", "gemini-2.0-flash"}, {"google", "gemini-2.0-flash-001"},
		{"xai", "grok-4"}, {"xai", "grok-4-0709"}, {"xai", "grok-3-mini"},
		{"groq", "llama-3.3-70b-versatile"}, {"groq", "llama-3.1-8b-instant"}, {"groq", "openai/gpt-oss-120b"},
		{"mistral", "mistral-large-latest"}, {"deepseek", "deepseek-chat"}, {"deepseek", "deepseek-reasoner"},
		{"openrouter", "openai/gpt-4o"}, {"openrouter", "anthropic/claude-sonnet-4.5"},
		{"azure", "gpt-4o"}, {"bedrock", "us.anthropic.claude-sonnet-4-20250514-v1:0"}, {"vertex", "gemini-2.5-pro"},
		{"ollama", "llama3"},
	} {
		if !providers.PriceKnown(tc[0], tc[1]) {
			p, _ := providers.LookupPrice(tc[0], tc[1])
			t.Errorf("%s/%s not cap-safe (source %q)", tc[0], tc[1], p.Source)
		}
	}
	// Prefix guesses stay refused: gpt-5-pro is priced 12x gpt-5's output.
	if providers.PriceKnown("anthropic", "claude-opus-9-preview") {
		t.Error("family guess accepted as cap-safe")
	}
	if p, _ := providers.LookupPrice("anthropic", "claude-3-5-haiku-20241022"); p.In != 0.8 || p.Out != 4 {
		t.Errorf("curated price wrong: %+v", p)
	}
}
