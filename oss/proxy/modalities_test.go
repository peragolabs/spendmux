package proxy

import "testing"

func TestReportedAudioSeconds(t *testing.T) {
	t.Parallel()
	if s, ok := reportedAudioSeconds([]byte(`{"text":"hi","duration":12.5}`)); !ok || s != 12.5 {
		t.Fatalf("verbose_json duration: %v %v", s, ok)
	}
	if s, ok := reportedAudioSeconds([]byte(`{"text":"hi","usage":{"type":"duration","seconds":7}}`)); !ok || s != 7 {
		t.Fatalf("usage.seconds: %v %v", s, ok)
	}
	if _, ok := reportedAudioSeconds([]byte(`plain text transcript`)); ok {
		t.Fatal("non-JSON response has no duration")
	}
}

func TestModalityPricers(t *testing.T) {
	t.Parallel()
	if usd, ok := priceImages("openai", "dall-e-3", []byte(`{"prompt":"x","n":3,"size":"1024x1024","quality":"standard"}`)); !ok || usd != 120_000 {
		t.Fatalf("3 standard dall-e-3 images = %d (%v), want 120000", usd, ok)
	}
	if usd, ok := priceSpeech("openai", "tts-1", []byte(`{"input":"héllo"}`)); !ok || usd <= 0 {
		t.Fatalf("tts-1 should be priced per character, got %d (%v)", usd, ok)
	}
	if _, ok := priceImages("custom", "my-sdxl", []byte(`{"prompt":"x"}`)); ok {
		t.Fatal("custom image model must be unknown so USD caps can refuse it")
	}
	if transcriptionSeconds(0) != 1 || transcriptionSeconds(160_000) != 10 {
		t.Fatal("transcription estimate: 16 kB per second, at least 1s")
	}
}
