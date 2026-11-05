// Command pricegen regenerates internal/providers/prices.json from LiteLLM's
// MIT-licensed model cost map, keeping only the fields Spendmux prices with.
//
//	go run ./scripts/pricegen                     # fetch from GitHub
//	go run ./scripts/pricegen -src prices.json    # from a local copy
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"time"
)

const defaultSrc = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

// entry mirrors providers.tableEntry; all costs are USD per unit.
type entry struct {
	Provider   string  `json:"p"`
	Mode       string  `json:"m,omitempty"`
	In         float64 `json:"i,omitempty"`   // per input token
	Out        float64 `json:"o,omitempty"`   // per output token
	CacheRead  float64 `json:"cr,omitempty"`  // per cached input token
	CacheWrite float64 `json:"cw,omitempty"`  // per input token written to the cache
	Image      float64 `json:"img,omitempty"` // per generated image
	Pixel      float64 `json:"px,omitempty"`  // per generated pixel
	Char       float64 `json:"ch,omitempty"`
	Second     float64 `json:"s,omitempty"`
}

var keepModes = map[string]bool{
	"chat": true, "completion": true, "responses": true, "embedding": true,
	"image_generation": true, "audio_speech": true, "audio_transcription": true, "moderation": true,
}

func main() {
	src := flag.String("src", defaultSrc, "URL or path of model_prices_and_context_window.json")
	out := flag.String("out", "internal/providers/prices.json", "output path")
	flag.Parse()

	raw, err := read(*src)
	if err != nil {
		fail(err)
	}
	var in map[string]map[string]any
	if err := json.Unmarshal(raw, &in); err != nil {
		var loose map[string]json.RawMessage
		if err2 := json.Unmarshal(raw, &loose); err2 != nil {
			fail(err)
		}
		in = map[string]map[string]any{}
		for k, v := range loose {
			var m map[string]any
			if json.Unmarshal(v, &m) == nil {
				in[k] = m
			}
		}
	}

	table := map[string]entry{}
	for key, v := range in {
		mode, _ := v["mode"].(string)
		if key == "sample_spec" || !keepModes[mode] {
			continue
		}
		e := entry{
			Provider:   str(v["litellm_provider"]),
			Mode:       mode,
			In:         num(v["input_cost_per_token"]),
			Out:        num(v["output_cost_per_token"]),
			CacheRead:  num(v["cache_read_input_token_cost"]),
			CacheWrite: num(v["cache_creation_input_token_cost"]),
			// For image models LiteLLM puts the per-image price in either field.
			Image:  first(num(v["output_cost_per_image"]), num(v["input_cost_per_image"])),
			Pixel:  first(num(v["input_cost_per_pixel"]), num(v["output_cost_per_pixel"])),
			Char:   num(v["input_cost_per_character"]),
			Second: num(v["input_cost_per_second"]),
		}
		if e.Provider == "" {
			continue
		}
		table[key] = e
	}

	keys := make([]string, 0, len(table))
	for k := range table {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	f, err := os.Create(*out)
	if err != nil {
		fail(err)
	}
	defer f.Close()
	// One entry per line keeps diffs reviewable when prices change.
	fmt.Fprintf(f, "{\"_source\":%q,\n\"_generated\":%q,\n\"models\":{\n", defaultSrc, time.Now().UTC().Format("2006-01-02"))
	for i, k := range keys {
		b, _ := json.Marshal(table[k])
		kb, _ := json.Marshal(k)
		sep := ","
		if i == len(keys)-1 {
			sep = ""
		}
		fmt.Fprintf(f, "%s:%s%s\n", kb, b, sep)
	}
	fmt.Fprintln(f, "}}")
	fmt.Fprintf(os.Stderr, "wrote %d models to %s\n", len(keys), *out)
}

func read(src string) ([]byte, error) {
	if len(src) > 8 && (src[:7] == "http://" || src[:8] == "https://") {
		client := &http.Client{Timeout: 60 * time.Second}
		resp, err := client.Get(src) // #nosec G107 -- operator-supplied URL in a dev tool
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("fetch %s: %s", src, resp.Status)
		}
		return io.ReadAll(resp.Body)
	}
	return os.ReadFile(src) // #nosec G304 -- dev tool
}

func str(v any) string { s, _ := v.(string); return s }

func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}

func first(vals ...float64) float64 {
	for _, v := range vals {
		if v > 0 {
			return v
		}
	}
	return 0
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "pricegen:", err)
	os.Exit(1)
}
