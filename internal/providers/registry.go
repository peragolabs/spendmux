package providers

import (
	"fmt"
	"strings"
)

// Config describes an upstream OpenAI-compatible (or Anthropic / AWS / Vertex) endpoint.
type Config struct {
	ID      string
	Name    string
	BaseURL string
	// Category groups the keys UI (optgroups).
	Category string
	// AuthStyle: "bearer" (default), "x-api-key" (Anthropic), "api-key" (Azure),
	// "aws-sigv4" (Bedrock), "vertex" (Vertex AI service account).
	AuthStyle string
	// CredMode: "" (single API key), "aws" (access+secret+region), "vertex" (SA JSON+project+location).
	CredMode string
	// ChatPath relative to BaseURL
	ChatPath string
	// EmbedPath relative to BaseURL (empty = unsupported)
	EmbedPath string
	// ImagePath e.g. "/images/generations" (empty = unsupported)
	ImagePath string
	// SpeechPath e.g. "/audio/speech" (empty = unsupported)
	SpeechPath string
	// TranscriptionPath e.g. "/audio/transcriptions" (empty = unsupported; multipart)
	TranscriptionPath string
	// ModerationPath e.g. "/moderations" (empty = unsupported)
	ModerationPath string
	// NativeAnthropic uses Anthropic Messages API instead of OpenAI chat
	NativeAnthropic bool
	// RequiresBaseURL means the vault entry must supply an endpoint override
	RequiresBaseURL bool
	// ModelsPath is GET path for listing models (default "/models").
	// Set to "-" when the provider has no list API (use SuggestedModels only).
	ModelsPath string
}

// Category display order for the keys UI.
var categoryOrder = []string{
	"Frontier",
	"Fast inference",
	"Aggregators",
	"Regional",
	"Local",
	"Enterprise",
	"Custom",
}

var registry = []Config{
	// Frontier
	{ID: "openai", Name: "OpenAI", Category: "Frontier", BaseURL: "https://api.openai.com/v1", ChatPath: "/chat/completions", EmbedPath: "/embeddings", ImagePath: "/images/generations", SpeechPath: "/audio/speech", TranscriptionPath: "/audio/transcriptions", ModerationPath: "/moderations"},
	{ID: "anthropic", Name: "Anthropic", Category: "Frontier", BaseURL: "https://api.anthropic.com", AuthStyle: "x-api-key", ChatPath: "/v1/messages", NativeAnthropic: true, ModelsPath: "-"},
	{ID: "google", Name: "Google Gemini", Category: "Frontier", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", ChatPath: "/chat/completions", EmbedPath: "/embeddings", ImagePath: "/images/generations"},
	{ID: "xai", Name: "xAI (Grok)", Category: "Frontier", BaseURL: "https://api.x.ai/v1", ChatPath: "/chat/completions", ImagePath: "/images/generations"},
	{ID: "mistral", Name: "Mistral", Category: "Frontier", BaseURL: "https://api.mistral.ai/v1", ChatPath: "/chat/completions", EmbedPath: "/embeddings"},
	{ID: "deepseek", Name: "DeepSeek", Category: "Frontier", BaseURL: "https://api.deepseek.com/v1", ChatPath: "/chat/completions"},
	{ID: "cohere", Name: "Cohere", Category: "Frontier", BaseURL: "https://api.cohere.ai/compatibility/v1", ChatPath: "/chat/completions", EmbedPath: "/embeddings"},
	{ID: "ai21", Name: "AI21", Category: "Frontier", BaseURL: "https://api.ai21.com/studio/v1", ChatPath: "/chat/completions"},

	// Fast inference
	{ID: "groq", Name: "Groq", Category: "Fast inference", BaseURL: "https://api.groq.com/openai/v1", ChatPath: "/chat/completions", EmbedPath: "/embeddings", SpeechPath: "/audio/speech", TranscriptionPath: "/audio/transcriptions"},
	{ID: "cerebras", Name: "Cerebras", Category: "Fast inference", BaseURL: "https://api.cerebras.ai/v1", ChatPath: "/chat/completions"},
	{ID: "together", Name: "Together", Category: "Fast inference", BaseURL: "https://api.together.xyz/v1", ChatPath: "/chat/completions", EmbedPath: "/embeddings", ImagePath: "/images/generations"},
	{ID: "fireworks", Name: "Fireworks", Category: "Fast inference", BaseURL: "https://api.fireworks.ai/inference/v1", ChatPath: "/chat/completions", EmbedPath: "/embeddings", ImagePath: "/images/generations"},
	{ID: "novita", Name: "Novita", Category: "Fast inference", BaseURL: "https://api.novita.ai/v3/openai", ChatPath: "/chat/completions", EmbedPath: "/embeddings", ImagePath: "/images/generations"},
	{ID: "deepinfra", Name: "DeepInfra", Category: "Fast inference", BaseURL: "https://api.deepinfra.com/v1/openai", ChatPath: "/chat/completions", EmbedPath: "/embeddings", ImagePath: "/images/generations"},
	{ID: "sambanova", Name: "SambaNova", Category: "Fast inference", BaseURL: "https://api.sambanova.ai/v1", ChatPath: "/chat/completions"},
	{ID: "hyperbolic", Name: "Hyperbolic", Category: "Fast inference", BaseURL: "https://api.hyperbolic.xyz/v1", ChatPath: "/chat/completions", ImagePath: "/images/generations"},
	{ID: "nvidia", Name: "NVIDIA NIM", Category: "Fast inference", BaseURL: "https://integrate.api.nvidia.com/v1", ChatPath: "/chat/completions", EmbedPath: "/embeddings"},
	{ID: "friendli", Name: "Friendli", Category: "Fast inference", BaseURL: "https://api.friendli.ai/serverless/v1", ChatPath: "/chat/completions"},
	{ID: "lambda", Name: "Lambda Labs", Category: "Fast inference", BaseURL: "https://api.lambdalabs.com/v1", ChatPath: "/chat/completions"},
	{ID: "nebius", Name: "Nebius", Category: "Fast inference", BaseURL: "https://api.studio.nebius.ai/v1", ChatPath: "/chat/completions", EmbedPath: "/embeddings"},
	{ID: "lepton", Name: "Lepton", Category: "Fast inference", BaseURL: "https://api.lepton.ai/api/v1", ChatPath: "/chat/completions"},
	{ID: "scaleway", Name: "Scaleway", Category: "Fast inference", BaseURL: "https://api.scaleway.ai/v1", ChatPath: "/chat/completions"},

	// Aggregators
	{ID: "openrouter", Name: "OpenRouter", Category: "Aggregators", BaseURL: "https://openrouter.ai/api/v1", ChatPath: "/chat/completions", EmbedPath: "/embeddings", ImagePath: "/images/generations"},
	{ID: "perplexity", Name: "Perplexity", Category: "Aggregators", BaseURL: "https://api.perplexity.ai", ChatPath: "/chat/completions", ModelsPath: "-"},
	{ID: "huggingface", Name: "Hugging Face", Category: "Aggregators", BaseURL: "https://router.huggingface.co/v1", ChatPath: "/chat/completions"},
	{ID: "github", Name: "GitHub Models", Category: "Aggregators", BaseURL: "https://models.github.ai/inference", ChatPath: "/chat/completions"},
	{ID: "replicate", Name: "Replicate", Category: "Aggregators", BaseURL: "https://openai-proxy.replicate.com/v1", ChatPath: "/chat/completions", ImagePath: "/images/generations"},
	{ID: "voyage", Name: "Voyage", Category: "Aggregators", BaseURL: "https://api.voyageai.com/v1", ChatPath: "/chat/completions", EmbedPath: "/embeddings"},

	// Regional
	{ID: "moonshot", Name: "Moonshot (Kimi)", Category: "Regional", BaseURL: "https://api.moonshot.ai/v1", ChatPath: "/chat/completions"},
	{ID: "qwen", Name: "Qwen (DashScope)", Category: "Regional", BaseURL: "https://dashscope-intl.aliyuncs.com/compatible-mode/v1", ChatPath: "/chat/completions", EmbedPath: "/embeddings", ImagePath: "/images/generations"},
	{ID: "siliconflow", Name: "SiliconFlow", Category: "Regional", BaseURL: "https://api.siliconflow.cn/v1", ChatPath: "/chat/completions", EmbedPath: "/embeddings", ImagePath: "/images/generations"},
	{ID: "zhipu", Name: "Zhipu (GLM)", Category: "Regional", BaseURL: "https://open.bigmodel.cn/api/paas/v4", ChatPath: "/chat/completions", EmbedPath: "/embeddings", ImagePath: "/images/generations"},
	{ID: "minimax", Name: "MiniMax", Category: "Regional", BaseURL: "https://api.minimax.io/v1", ChatPath: "/chat/completions"},

	// Local (host.docker.internal reaches the Mac/Windows host from Docker Desktop)
	{ID: "ollama", Name: "Ollama", Category: "Local", BaseURL: "http://host.docker.internal:11434/v1", ChatPath: "/chat/completions", EmbedPath: "/embeddings"},
	{ID: "lmstudio", Name: "LM Studio", Category: "Local", BaseURL: "http://host.docker.internal:1234/v1", ChatPath: "/chat/completions", EmbedPath: "/embeddings"},
	{ID: "vllm", Name: "vLLM", Category: "Local", BaseURL: "", ChatPath: "/chat/completions", EmbedPath: "/embeddings", RequiresBaseURL: true},

	// Enterprise — endpoint depends on your resource / account
	{ID: "azure", Name: "Azure OpenAI", Category: "Enterprise", BaseURL: "", AuthStyle: "api-key", ChatPath: "/chat/completions", EmbedPath: "/embeddings", ImagePath: "/images/generations", SpeechPath: "/audio/speech", TranscriptionPath: "/audio/transcriptions", ModerationPath: "/moderations", RequiresBaseURL: true},
	{ID: "azure-ai", Name: "Azure AI Foundry", Category: "Enterprise", BaseURL: "", AuthStyle: "api-key", ChatPath: "/chat/completions", EmbedPath: "/embeddings", ImagePath: "/images/generations", SpeechPath: "/audio/speech", TranscriptionPath: "/audio/transcriptions", ModerationPath: "/moderations", RequiresBaseURL: true},
	{ID: "bedrock", Name: "AWS Bedrock", Category: "Enterprise", BaseURL: "", AuthStyle: "aws-sigv4", CredMode: "aws", ChatPath: "/chat/completions", EmbedPath: "/embeddings"},
	{ID: "vertex", Name: "Google Vertex AI", Category: "Enterprise", BaseURL: "", AuthStyle: "vertex", CredMode: "vertex", ChatPath: "/chat/completions", EmbedPath: "/embeddings", ImagePath: "/images/generations"},
	{ID: "databricks", Name: "Databricks", Category: "Enterprise", BaseURL: "", ChatPath: "/chat/completions", EmbedPath: "/embeddings", RequiresBaseURL: true},
	{ID: "cloudflare", Name: "Cloudflare Workers AI", Category: "Enterprise", BaseURL: "", ChatPath: "/chat/completions", EmbedPath: "/embeddings", ImagePath: "/images/generations", RequiresBaseURL: true},

	// Catch-all OpenAI-compatible endpoint
	{ID: "custom", Name: "Custom OpenAI-compatible", Category: "Custom", BaseURL: "", ChatPath: "/chat/completions", EmbedPath: "/embeddings", ImagePath: "/images/generations", SpeechPath: "/audio/speech", TranscriptionPath: "/audio/transcriptions", ModerationPath: "/moderations", RequiresBaseURL: true},
}

// Group is a labeled set of providers for the keys UI.
type Group struct {
	Label string
	Items []Config
}

// All returns known providers for the keys UI.
func All() []Config {
	out := make([]Config, len(registry))
	copy(out, registry)
	return out
}

// Groups returns providers organized by category for optgroups.
func Groups() []Group {
	byCat := map[string][]Config{}
	for _, p := range registry {
		byCat[p.Category] = append(byCat[p.Category], p)
	}
	out := make([]Group, 0, len(categoryOrder))
	for _, label := range categoryOrder {
		if items := byCat[label]; len(items) > 0 {
			out = append(out, Group{Label: label, Items: items})
		}
	}
	return out
}

func Get(id string) (Config, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, p := range registry {
		if p.ID == id {
			return p, true
		}
	}
	return Config{}, false
}

// ResolveRoute requires explicit provider + model (dev API — no legacy fallback).
// Model may contain slashes (e.g. openai/gpt-oss-120b).
func ResolveRoute(provider, model string) (providerID, modelName string, err error) {
	return ResolveRouteWithDefault(provider, model, "")
}

// ResolveRouteWithDefault resolves the upstream when the client cannot send a
// separate "provider" field (Cursor, Cline, and other model-name-only clients).
// Precedence: explicit provider → defaultProvider (the token's provider scope)
// → "provider/model" shorthand when the prefix is a known provider id.
func ResolveRouteWithDefault(provider, model, defaultProvider string) (providerID, modelName string, err error) {
	providerID = strings.ToLower(strings.TrimSpace(provider))
	modelName = strings.TrimSpace(model)
	if providerID == "" {
		providerID = strings.ToLower(strings.TrimSpace(defaultProvider))
	}
	if providerID == "" {
		if head, tail, ok := strings.Cut(modelName, "/"); ok {
			if _, known := Get(strings.ToLower(head)); known && tail != "" {
				providerID, modelName = strings.ToLower(head), tail
			}
		}
	}
	if providerID == "" {
		return "", "", fmt.Errorf(`"provider" is required (e.g. "groq", "cerebras") — or scope this token to a provider on /keys, or use "provider/model" as the model name`)
	}
	if modelName == "" {
		return "", "", fmt.Errorf(`"model" is required (upstream model id, may include slashes)`)
	}
	if _, ok := Get(providerID); !ok {
		return "", "", fmt.Errorf("unknown provider: %s", providerID)
	}
	return providerID, modelName, nil
}

// SuggestedModels are example model ids per provider for the playground picker.
// Spendmux does not host these — users bring their own keys and pick any id their provider accepts.
func SuggestedModels(providerID string) []string {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	switch providerID {
	case "openai":
		return []string{"gpt-4.1-mini", "gpt-4.1", "o4-mini"}
	case "anthropic":
		return []string{"claude-sonnet-4-20250514", "claude-haiku-4-5-20251001"}
	case "google":
		return []string{"gemini-3.6-flash"}
	case "xai":
		return []string{"grok-3-mini", "grok-3"}
	case "mistral":
		return []string{"mistral-small-latest", "mistral-large-latest"}
	case "deepseek":
		return []string{"deepseek-chat", "deepseek-reasoner"}
	case "ai21":
		return []string{"jamba-large", "jamba-mini"}
	case "groq":
		return []string{
			"openai/gpt-oss-120b",
			"openai/gpt-oss-20b",
			"llama-3.3-70b-versatile",
			"llama-3.1-8b-instant",
			"qwen/qwen3.6-27b",
			"meta-llama/llama-4-scout-17b-16e-instruct",
		}
	case "cerebras":
		return []string{"llama-3.3-70b", "llama3.1-8b"}
	case "together":
		return []string{"meta-llama/Meta-Llama-3.1-70B-Instruct-Turbo", "Qwen/Qwen2.5-72B-Instruct-Turbo"}
	case "fireworks":
		return []string{"accounts/fireworks/models/llama-v3p3-70b-instruct"}
	case "novita":
		return []string{"meta-llama/llama-3.3-70b-instruct", "deepseek/deepseek-v3-0324"}
	case "lambda":
		return []string{"llama3.1-70b-instruct-fp8", "llama3.1-8b-instruct"}
	case "nebius":
		return []string{"meta-llama/Meta-Llama-3.1-70B-Instruct", "Qwen/Qwen2.5-72B-Instruct"}
	case "openrouter":
		return []string{"openai/gpt-4.1-mini", "anthropic/claude-sonnet-4", "google/gemini-2.5-flash"}
	case "perplexity":
		return []string{"sonar", "sonar-pro"}
	case "github":
		return []string{"openai/gpt-4.1-mini", "openai/gpt-4o"}
	case "replicate":
		return []string{"meta/meta-llama-3.1-70b-instruct"}
	case "voyage":
		return []string{"voyage-3", "voyage-code-3"}
	case "zhipu":
		return []string{"glm-4-flash", "glm-4-plus"}
	case "minimax":
		return []string{"MiniMax-Text-01", "MiniMax-M1"}
	case "ollama":
		return []string{"llama3.2", "mistral", "qwen2.5"}
	case "lmstudio":
		return []string{"local-model"}
	case "vllm":
		return []string{"meta-llama/Meta-Llama-3.1-8B-Instruct"}
	case "bedrock":
		return []string{"amazon.nova-lite-v1:0", "anthropic.claude-3-5-sonnet-20241022-v2:0", "meta.llama3-70b-instruct-v1:0"}
	case "vertex":
		return []string{"google/gemini-2.0-flash-001", "google/gemini-2.5-pro"}
	case "databricks":
		return []string{"databricks-meta-llama-3-3-70b-instruct", "databricks-claude-sonnet-4"}
	case "azure", "azure-ai":
		return []string{"gpt-4o", "gpt-4o-mini"}
	default:
		return nil
	}
}
