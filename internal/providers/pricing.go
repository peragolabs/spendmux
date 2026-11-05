package providers

import (
	_ "embed"
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Pricing is list-price only: Spendmux never marks up tokens, so caps are
// enforced against what the provider would bill.
//
// Lookup order for (provider, model):
//  1. prices.json, generated from LiteLLM's MIT-licensed cost map
//     (go run ./scripts/pricegen), under the provider's LiteLLM names
//  2. the same with a trailing date stamp removed (gpt-4o-2024-08-06 → gpt-4o)
//  3. curated model families, longest prefix first
//  4. a per-provider default for models newer than the table
//  5. local runtimes (Ollama, LM Studio, vLLM) are free
//
// Anything else is unknown: EstimateCostUSDMicros returns 0 and PriceKnown
// reports false, so callers can refuse it on a USD-capped token instead of
// letting it through uncounted.

//go:embed prices.json
var pricesJSON []byte

// Price is a model's list price. Token prices are USD per 1M tokens; the
// rest are USD per unit.
type Price struct {
	In         float64
	Out        float64
	CacheRead  float64
	CacheWrite float64
	PerImage   float64
	PerChar    float64
	PerSecond  float64
	Source     string // "table", "curated", "family", "provider-default", "local"
}

type tableEntry struct {
	Provider   string  `json:"p"`
	Mode       string  `json:"m"`
	In         float64 `json:"i"`
	Out        float64 `json:"o"`
	CacheRead  float64 `json:"cr"`
	CacheWrite float64 `json:"cw"`
	Image      float64 `json:"img"`
	Pixel      float64 `json:"px"`
	Char       float64 `json:"ch"`
	Second     float64 `json:"s"`
}

func (e tableEntry) price() Price {
	return Price{
		In: e.In * 1e6, Out: e.Out * 1e6, CacheRead: e.CacheRead * 1e6, CacheWrite: e.CacheWrite * 1e6,
		PerImage: e.Image, PerChar: e.Char, PerSecond: e.Second, Source: "table",
	}
}

var (
	tableOnce sync.Once
	// table[litellmProvider][lowercased model without provider prefix]
	table map[string]map[string]tableEntry
)

func loadTable() {
	tableOnce.Do(func() {
		var doc struct {
			Models map[string]tableEntry `json:"models"`
		}
		table = map[string]map[string]tableEntry{}
		if err := json.Unmarshal(pricesJSON, &doc); err != nil {
			return
		}
		for key, e := range doc.Models {
			bare := strings.ToLower(key)
			bare = strings.TrimPrefix(bare, strings.ToLower(e.Provider)+"/")
			if strings.HasPrefix(e.Provider, "vertex_ai") {
				bare = strings.TrimPrefix(bare, "vertex_ai/")
			}
			if table[e.Provider] == nil {
				table[e.Provider] = map[string]tableEntry{}
			}
			if _, dup := table[e.Provider][bare]; !dup || key == bare {
				table[e.Provider][bare] = e
			}
		}
	})
}

// litellmProviders maps Spendmux provider ids to the provider names used in
// prices.json, most specific first.
var litellmProviders = map[string][]string{
	"openai":      {"openai", "text-completion-openai"},
	"anthropic":   {"anthropic"},
	"google":      {"gemini"},
	"xai":         {"xai"},
	"mistral":     {"mistral"},
	"deepseek":    {"deepseek"},
	"cohere":      {"cohere_chat", "cohere"},
	"ai21":        {"ai21"},
	"groq":        {"groq"},
	"cerebras":    {"cerebras"},
	"together":    {"together_ai"},
	"fireworks":   {"fireworks_ai", "fireworks_ai-embedding-models"},
	"novita":      {"novita"},
	"deepinfra":   {"deepinfra"},
	"sambanova":   {"sambanova"},
	"hyperbolic":  {"hyperbolic"},
	"nvidia":      {"nvidia_nim"},
	"friendli":    {"friendliai"},
	"lambda":      {"lambda_ai"},
	"nebius":      {"nebius"},
	"lepton":      {"lepton"},
	"scaleway":    {"scaleway"},
	"openrouter":  {"openrouter"},
	"perplexity":  {"perplexity"},
	"huggingface": {"huggingface"},
	"github":      {"github"},
	"replicate":   {"replicate"},
	"voyage":      {"voyage"},
	"moonshot":    {"moonshot"},
	"qwen":        {"dashscope"},
	"siliconflow": {"siliconflow"},
	"zhipu":       {"zai"},
	"minimax":     {"minimax"},
	"azure":       {"azure", "openai"},
	"azure-ai":    {"azure_ai"},
	"bedrock":     {"bedrock_converse", "bedrock"},
	"vertex":      {"vertex_ai-language-models", "vertex_ai", "vertex_ai-anthropic_models", "vertex_ai-mistral_models", "vertex_ai-llama_models", "vertex_ai-embedding-models", "gemini"},
	"databricks":  {"databricks"},
	"cloudflare":  {"cloudflare"},
}

// Local runtimes run on the user's own hardware; their tokens cost nothing.
var localProviders = map[string]bool{"ollama": true, "lmstudio": true, "vllm": true}

var dateSuffix = regexp.MustCompile(`-(\d{4}-\d{2}-\d{2}|\d{8}|\d{4})$`)

func modelCandidates(model string) []string {
	m := strings.ToLower(strings.TrimSpace(model))
	out := []string{m}
	if s := dateSuffix.ReplaceAllString(m, ""); s != m {
		out = append(out, s)
	}
	if i := strings.LastIndex(m, "/"); i >= 0 && i < len(m)-1 {
		last := m[i+1:]
		out = append(out, last)
		if s := dateSuffix.ReplaceAllString(last, ""); s != last {
			out = append(out, s)
		}
	}
	return out
}

func tableLookup(provider string, candidates []string) (tableEntry, bool) {
	loadTable()
	for _, p := range litellmProviders[provider] {
		models := table[p]
		for _, c := range candidates {
			if e, ok := models[c]; ok {
				return e, true
			}
		}
	}
	return tableEntry{}, false
}

// LookupPrice returns the list price for provider/model and whether one is known.
func LookupPrice(provider, model string) (Price, bool) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	cands := modelCandidates(model)
	if e, ok := tableLookup(provider, cands); ok {
		return e.price(), true
	}
	if p, ok := curatedLookup(provider, cands); ok {
		return p, true
	}
	if localProviders[provider] {
		return Price{Source: "local"}, true
	}
	if p, ok := familyPrice(cands); ok {
		return p, true
	}
	if p, ok := providerDefaults[provider]; ok {
		p.Source = "provider-default"
		return p, true
	}
	return Price{}, false
}

// PriceKnown reports whether Spendmux can put a cap-safe dollar figure on
// this model: an exact table or curated price, or a free local runtime.
// Family-prefix and provider-default guesses are not cap-safe (a prefix like
// "gpt-5" also matches "gpt-5-pro" at 12x the output price), so a USD-capped
// key refuses them instead of under-reserving.
func PriceKnown(provider, model string) bool {
	p, ok := LookupPrice(provider, model)
	return ok && (p.Source == "table" || p.Source == "curated" || p.Source == "local")
}

// curatedPrices are exact list prices (USD per 1M tokens) for widely used
// models that are still served but have dropped out of the generated table,
// plus their "-latest"/"-0" aliases. Keyed by Spendmux provider id, then the
// lower-cased model (date stamps are stripped by modelCandidates). Keep in
// sync with provider price pages; see docs/RELEASE-CHECKLIST-2026-10.md.
var curatedPrices = map[string]map[string]Price{
	"anthropic": {
		"claude-opus-4":            {In: 15, Out: 75, CacheRead: 1.5, CacheWrite: 18.75},
		"claude-opus-4-0":          {In: 15, Out: 75, CacheRead: 1.5, CacheWrite: 18.75},
		"claude-opus-4-1":          {In: 15, Out: 75, CacheRead: 1.5, CacheWrite: 18.75},
		"claude-sonnet-4":          {In: 3, Out: 15, CacheRead: 0.3, CacheWrite: 3.75},
		"claude-sonnet-4-0":        {In: 3, Out: 15, CacheRead: 0.3, CacheWrite: 3.75},
		"claude-3-7-sonnet":        {In: 3, Out: 15, CacheRead: 0.3, CacheWrite: 3.75},
		"claude-3-7-sonnet-latest": {In: 3, Out: 15, CacheRead: 0.3, CacheWrite: 3.75},
		"claude-3-5-sonnet":        {In: 3, Out: 15, CacheRead: 0.3, CacheWrite: 3.75},
		"claude-3-5-sonnet-latest": {In: 3, Out: 15, CacheRead: 0.3, CacheWrite: 3.75},
		"claude-3-5-haiku":         {In: 0.8, Out: 4, CacheRead: 0.08, CacheWrite: 1},
		"claude-3-5-haiku-latest":  {In: 0.8, Out: 4, CacheRead: 0.08, CacheWrite: 1},
		"claude-3-haiku":           {In: 0.25, Out: 1.25, CacheRead: 0.03, CacheWrite: 0.3},
		"claude-3-opus":            {In: 15, Out: 75, CacheRead: 1.5, CacheWrite: 18.75},
		"claude-3-opus-latest":     {In: 15, Out: 75, CacheRead: 1.5, CacheWrite: 18.75},
	},
	"openai": {
		"chatgpt-4o-latest": {In: 5, Out: 15},
	},
	"xai": {
		"grok-4":      {In: 3, Out: 15},
		"grok-4-0709": {In: 3, Out: 15},
		"grok-3":      {In: 3, Out: 15},
		"grok-3-mini": {In: 0.3, Out: 0.5},
	},
	"google": {
		// Upper (long-context) tier where the price is tiered.
		"gemini-2.0-flash":          {In: 0.1, Out: 0.4},
		"gemini-2.0-flash-lite":     {In: 0.075, Out: 0.3},
		"gemini-2.0-flash-001":      {In: 0.1, Out: 0.4},
		"gemini-2.0-flash-lite-001": {In: 0.075, Out: 0.3},
		"gemini-1.5-pro":            {In: 2.5, Out: 10},
		"gemini-1.5-flash":          {In: 0.15, Out: 0.6},
	},
	"groq": {
		"llama-3.3-70b-versatile": {In: 0.59, Out: 0.79},
		"llama-3.1-8b-instant":    {In: 0.05, Out: 0.08},
	},
}

func curatedLookup(provider string, cands []string) (Price, bool) {
	models := curatedPrices[provider]
	for _, c := range cands {
		if p, ok := models[c]; ok {
			p.Source = "curated"
			return p, true
		}
	}
	return Price{}, false
}

// EstimateCostUSDMicros returns microdollars (USD * 1e6) for a token-priced call.
func EstimateCostUSDMicros(provider, model string, promptTokens, completionTokens int) int64 {
	p, _ := LookupPrice(provider, model)
	in := (float64(promptTokens) / 1_000_000) * p.In
	out := (float64(completionTokens) / 1_000_000) * p.Out
	return toMicros(in + out)
}

// EstimateReservationUSDMicros reserves the highest known prompt rate,
// including cache writes. Generic family/provider guesses are not cap-safe;
// callers must also require PriceKnown. Rates still require operational upkeep.
func EstimateReservationUSDMicros(provider, model string, promptTokens, completionTokens int) int64 {
	p, _ := LookupPrice(provider, model)
	inputRate := max(p.In, p.CacheRead, p.CacheWrite)
	if strings.EqualFold(strings.TrimSpace(provider), "anthropic") {
		inputRate = max(inputRate, 2*p.In)
	}
	return toMicros((float64(promptTokens)*inputRate + float64(completionTokens)*p.Out) / 1_000_000)
}

// EstimateCachedCostUSDMicros prices a call whose prompt was partly served
// from (or written to) the provider's prompt cache. promptTokens includes
// both. A model without a cache-read price is billed at the input price, and
// one without a cache-write price at the input price (Anthropic: 1.25x, its
// published premium for a 5-minute write).
func EstimateCachedCostUSDMicros(provider, model string, promptTokens, completionTokens, cacheRead, cacheWrite int) int64 {
	p, _ := LookupPrice(provider, model)
	if cacheRead < 0 {
		cacheRead = 0
	}
	if cacheWrite < 0 {
		cacheWrite = 0
	}
	uncached := promptTokens - cacheRead - cacheWrite
	if uncached < 0 {
		uncached = 0
	}
	readPrice, writePrice := p.CacheRead, p.CacheWrite
	if readPrice <= 0 {
		readPrice = p.In
	}
	if writePrice <= 0 {
		writePrice = p.In
		if strings.EqualFold(strings.TrimSpace(provider), "anthropic") {
			writePrice = p.In * 1.25
		}
	}
	usd := (float64(uncached)*p.In + float64(cacheRead)*readPrice + float64(cacheWrite)*writePrice + float64(completionTokens)*p.Out) / 1_000_000
	return toMicros(usd)
}

// EstimateImageUSDMicros prices n generated images. size is "1024x1024"
// style and quality "standard"/"hd"/"low"/"medium"/"high"; both may be empty.
func EstimateImageUSDMicros(provider, model string, n int, size, quality string) (int64, bool) {
	if n < 1 {
		n = 1
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	m := strings.ToLower(strings.TrimSpace(model))
	size = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(size)), "x", "-x-")
	quality = strings.ToLower(strings.TrimSpace(quality))
	if size == "" {
		size = "1024-x-1024"
	}
	var cands []string
	if quality != "" {
		cands = append(cands, quality+"/"+size+"/"+m)
	}
	// Without a quality, price at the most expensive tier rather than the cheapest.
	cands = append(cands, "hd/"+size+"/"+m, "high/"+size+"/"+m, size+"/"+m, "standard/"+size+"/"+m, "medium/"+size+"/"+m, m)
	if e, ok := tableLookup(provider, cands); ok {
		if e.Image > 0 {
			return toMicros(float64(n) * e.Image), true
		}
		if e.Pixel > 0 {
			return toMicros(float64(n) * float64(pixels(size)) * e.Pixel), true
		}
	}
	for _, q := range []string{quality, "hd"} {
		if per, ok := imageOverrides[provider+"/"+q+"/"+size+"/"+m]; ok && q != "" {
			return toMicros(float64(n) * per), true
		}
	}
	if localProviders[provider] {
		return 0, true
	}
	return 0, false
}

// EstimateSpeechUSDMicros prices text-to-speech by input characters.
func EstimateSpeechUSDMicros(provider, model string, chars int) (int64, bool) {
	p, ok := LookupPrice(provider, model)
	if !ok {
		return 0, false
	}
	switch {
	case p.PerChar > 0:
		return toMicros(float64(chars) * p.PerChar), true
	case p.In > 0 || p.Out > 0:
		// Token-priced TTS (gpt-4o-mini-tts): ~4 chars per input token, and
		// audio output is billed on top; count output at the same volume.
		tok := float64(chars)/4 + 1
		return toMicros(tok/1e6*p.In + tok/1e6*p.Out), true
	}
	return 0, p.Source == "local"
}

// EstimateTranscriptionUSDMicros prices speech-to-text by audio seconds.
func EstimateTranscriptionUSDMicros(provider, model string, seconds float64) (int64, bool) {
	p, ok := LookupPrice(provider, model)
	if !ok {
		return 0, false
	}
	if p.PerSecond > 0 {
		return toMicros(seconds * p.PerSecond), true
	}
	if p.Source == "local" {
		return 0, true
	}
	if p.Source == "table" && (p.In > 0 || p.Out > 0) {
		// Token-priced transcription (gpt-4o-transcribe): audio is ~10 tokens/s
		// in (approximate) plus a short text output.
		tok := seconds * 10
		return toMicros(tok/1e6*p.In + tok/1e6*p.Out), true
	}
	return 0, false
}

// imageOverrides price image models the generated table no longer lists.
// Keys are provider/quality/size/model; USD per image.
var imageOverrides = map[string]float64{
	"openai/standard/1024-x-1024/dall-e-3": 0.04,
	"openai/standard/1024-x-1792/dall-e-3": 0.08,
	"openai/standard/1792-x-1024/dall-e-3": 0.08,
	"openai/hd/1024-x-1024/dall-e-3":       0.08,
	"openai/hd/1024-x-1792/dall-e-3":       0.12,
	"openai/hd/1792-x-1024/dall-e-3":       0.12,
	"openai/hd/256-x-256/dall-e-2":         0.016,
	"openai/hd/512-x-512/dall-e-2":         0.018,
	"openai/hd/1024-x-1024/dall-e-2":       0.02,
	"openai/standard/256-x-256/dall-e-2":   0.016,
	"openai/standard/512-x-512/dall-e-2":   0.018,
	"openai/standard/1024-x-1024/dall-e-2": 0.02,
}

func pixels(size string) int {
	var w, h int
	if parts := strings.Split(size, "-x-"); len(parts) == 2 {
		w, _ = strconv.Atoi(parts[0])
		h, _ = strconv.Atoi(parts[1])
	}
	if w <= 0 || h <= 0 {
		return 1024 * 1024
	}
	return w * h
}

func toMicros(usd float64) int64 {
	if usd <= 0 {
		return 0
	}
	// Round away float noise (15000.000000002) before rounding partial micros up.
	return int64(math.Ceil(math.Round(usd*1e9) / 1e3))
}

// familyPrices cover models newer than prices.json. Matched longest prefix
// first against each model candidate, so "gpt-4o-mini" never resolves to
// "gpt-4o".
var familyPrices = map[string]Price{
	"gpt-4o-mini":      {In: 0.15, Out: 0.60},
	"gpt-4o":           {In: 2.50, Out: 10.00},
	"gpt-4.1-nano":     {In: 0.10, Out: 0.40},
	"gpt-4.1-mini":     {In: 0.40, Out: 1.60},
	"gpt-4.1":          {In: 2.00, Out: 8.00},
	"gpt-5-nano":       {In: 0.05, Out: 0.40},
	"gpt-5-mini":       {In: 0.25, Out: 2.00},
	"gpt-5":            {In: 1.25, Out: 10.00},
	"o1-mini":          {In: 1.10, Out: 4.40},
	"o1":               {In: 15.00, Out: 60.00},
	"o3-mini":          {In: 1.10, Out: 4.40},
	"o3":               {In: 2.00, Out: 8.00},
	"o4-mini":          {In: 1.10, Out: 4.40},
	"claude-opus":      {In: 15.00, Out: 75.00},
	"claude-sonnet":    {In: 3.00, Out: 15.00},
	"claude-haiku":     {In: 0.80, Out: 4.00},
	"claude-3-opus":    {In: 15.00, Out: 75.00},
	"gemini-2.5-pro":   {In: 1.25, Out: 10.00},
	"gemini-2.5-flash": {In: 0.30, Out: 2.50},
	"gpt-oss-120b":     {In: 0.15, Out: 0.75},
	"gpt-oss-20b":      {In: 0.10, Out: 0.50},
	"llama-3.1-8b":     {In: 0.05, Out: 0.08},
	"llama3.1-8b":      {In: 0.05, Out: 0.08},
	"llama-3.3-70b":    {In: 0.59, Out: 0.79},
	"llama3.1-70b":     {In: 0.85, Out: 1.20},
}

var (
	familyOnce sync.Once
	familyKeys []string
)

func familyPrice(cands []string) (Price, bool) {
	familyOnce.Do(func() {
		for k := range familyPrices {
			familyKeys = append(familyKeys, k)
		}
		sort.Slice(familyKeys, func(i, j int) bool {
			if len(familyKeys[i]) != len(familyKeys[j]) {
				return len(familyKeys[i]) > len(familyKeys[j])
			}
			return familyKeys[i] < familyKeys[j]
		})
	})
	for _, c := range cands {
		for _, k := range familyKeys {
			if strings.HasPrefix(c, k) {
				p := familyPrices[k]
				p.Source = "family"
				return p, true
			}
		}
	}
	return Price{}, false
}

// providerDefaults price a model the table and families don't know yet. They
// lean toward each provider's flagship tier so a new model is over- rather
// than under-counted against a cap.
var providerDefaults = map[string]Price{
	"openai":      {In: 2.50, Out: 10.00},
	"anthropic":   {In: 3.00, Out: 15.00},
	"google":      {In: 1.25, Out: 10.00},
	"xai":         {In: 3.00, Out: 15.00},
	"mistral":     {In: 2.00, Out: 6.00},
	"deepseek":    {In: 0.55, Out: 2.19},
	"cohere":      {In: 2.50, Out: 10.00},
	"ai21":        {In: 2.00, Out: 8.00},
	"groq":        {In: 0.60, Out: 0.80},
	"cerebras":    {In: 0.85, Out: 1.20},
	"together":    {In: 0.90, Out: 0.90},
	"fireworks":   {In: 0.90, Out: 0.90},
	"novita":      {In: 0.60, Out: 0.80},
	"deepinfra":   {In: 0.60, Out: 0.80},
	"sambanova":   {In: 0.60, Out: 1.20},
	"hyperbolic":  {In: 0.40, Out: 0.40},
	"nvidia":      {In: 0.60, Out: 0.80},
	"friendli":    {In: 0.60, Out: 0.80},
	"lambda":      {In: 0.60, Out: 0.80},
	"nebius":      {In: 0.60, Out: 1.80},
	"lepton":      {In: 0.60, Out: 0.80},
	"scaleway":    {In: 0.90, Out: 0.90},
	"perplexity":  {In: 3.00, Out: 15.00},
	"huggingface": {In: 0.60, Out: 0.80},
	"github":      {In: 2.50, Out: 10.00},
	"replicate":   {In: 0.90, Out: 0.90},
	"voyage":      {In: 0.18, Out: 0},
	"moonshot":    {In: 0.60, Out: 2.50},
	"qwen":        {In: 1.60, Out: 6.40},
	"siliconflow": {In: 0.60, Out: 0.80},
	"zhipu":       {In: 0.60, Out: 2.20},
	"minimax":     {In: 0.40, Out: 2.20},
	"azure":       {In: 2.50, Out: 10.00},
	"azure-ai":    {In: 2.50, Out: 10.00},
	"bedrock":     {In: 3.00, Out: 15.00},
	"vertex":      {In: 1.25, Out: 10.00},
	"databricks":  {In: 2.50, Out: 10.00},
	"cloudflare":  {In: 0.30, Out: 2.00},
}
