# Spendmux

Self-hosted **BYOK spend control** for LLM apps. Add your own provider keys once, get **one OpenAI-compatible URL** and **one bearer token per app** with dollar caps, and see where the money goes, per app, provider and model.

- **Encrypted vault.** Provider keys are sealed in the browser, AES-256-GCM encrypted again at rest, and decrypted in memory only to sign the outbound request. Never logged, never rendered.
- **Wallets on your keys.** Day / week / month / session USD caps, tenant caps, per-token RPM/TPM, and a loop detector, all enforced *before* the provider is billed.
- **Proxy.** `/v1/chat/completions`, `/v1/messages` (Anthropic format), `/v1/responses` (OpenAI Responses format), `/v1/embeddings`, `/v1/images/generations`, `/v1/audio/*`, `/v1/moderations` and `/v1/models` on 40+ providers, with several keys per provider, explicit fallback ladders and a playground.
- **Savings.** Prompt caching on by default, opt-in response caching, opt-in conversation memory.
- **Visibility.** A usage log by provider and request, and OpenTelemetry traces.
- **Resilience.** Usage writes go through a supervised actor (panic → restart; HTTP keeps serving), and deploys drain in-flight streams.

MIT licensed. The hosted version at **[spendmux.com](https://spendmux.com)** runs this for you, keeps provider keys in a relay in your own Cloudflare account, and adds a spend observatory, spend alerts, teams and company SSO.

## Layout

```
cmd/server/          Entrypoint: env → config validation → migrations → actor supervisor → Fiber app
internal/router/     All HTTP routes (router.go)
internal/pages/      Serves the SPA shell per page id (server-owned routing)
internal/auth/       OAuth sign-in, DB sessions, GetUserID seam, account deletion, settings
internal/playground/ BYOK chat: JSON bootstrap + WebSocket stream
oss/proxy/           The /v1 proxy: routing, upstream auth, streaming, fallback, caching
oss/keys/            Vault, bearer tokens, caps/budgets, scopes, loop detector, rate limits, ladder
oss/usage/           Usage log API
oss/memory/          Encrypted conversation memory
internal/providers/  Provider registry, pricing (prices.json from LiteLLM's cost map), AWS SigV4 / Vertex auth
internal/upstream/   Hardened outbound client (SSRF guard, timeouts, per-key circuit breaker)
internal/telemetry/  OpenTelemetry tracing (off unless OTEL_EXPORTER_OTLP_* is set)
internal/crypto/     AES-GCM vault, token hashing     internal/actor/ supervisor/  Supervised writes
internal/database/   GORM + advisory-locked AutoMigrate     models/  GORM models
middleware/          Security headers + same-origin (production.go), auth guards, IP rate limit
frontend/react/      Vite + React SPA → priv/www (keys, playground, usage, fallback, account, landing, legal)
docs/                production.md (runbook)
```

**Routing model:** the server owns URLs. Each page handler serves the SPA shell with `data-app-id="<page>"`;
`frontend/react/src/main.tsx` mounts the matching component. Pages talk to `/api/*` JSON endpoints with the
session cookie; apps talk to `/v1/*` with a bearer token.

## Local development

```bash
cp .env.example .env               # fill in secrets (see below)
docker compose -f docker-compose.localhost.yml up -d postgres
make frontend-install frontend     # once, and whenever frontend/ changes
go run ./cmd/server                # http://localhost:8080
```

Minimum `.env` for development:

```bash
APP_ENV=development
PORT=8080
BASE_URL=http://localhost:8080
DATABASE_URL=postgres://postgres:spendmux_dev_change_me@127.0.0.1:5432/spendmux?sslmode=disable
COOKIE_SECRET=$(openssl rand -base64 32)          # sessions, email hashes
KEY_ENCRYPTION_SECRET=$(openssl rand -base64 32)  # vault — a separate value; losing it loses the vault
GITHUB_CLIENT_ID=… GITHUB_CLIENT_SECRET=…         # and/or Google, Apple
```

Then sign in, add a provider key and mint a token on `/keys`, and call the proxy:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-YOUR_SPENDMUX_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"provider": "groq", "model": "openai/gpt-oss-120b", "messages": [{"role": "user", "content": "hi"}]}'
```

Tests that need Postgres skip unless `TEST_DATABASE_URL` points at a scratch database.

OAuth callback URLs are `${BASE_URL}/auth/<provider>/callback` (register those in each console; the legacy
`/oauth/<provider>/callback` path is also routed). `make test`, `make race`, `make vet`, `make security`,
`make vuln`, `make fmt-check` mirror the CI gates.

## Model prices

USD caps are enforced against list prices from `internal/providers/prices.json`, generated from
[LiteLLM's MIT-licensed cost map](https://github.com/BerriAI/litellm/blob/main/model_prices_and_context_window.json).
Refresh it with `go run ./scripts/pricegen` and commit the diff. A model with no known price is refused on a
token with a USD cap (`400 model_price_unknown`) rather than passed through uncounted.

## Several keys per provider

Save more than one key for a provider on `/keys` and each request picks one by weight (1-100; a key with
weight 3 next to one with weight 1 takes about 75% of calls). A key that answers 429, 401/403, a timeout
or a 5xx cools down (Retry-After is honoured, capped at 10 minutes; auth failures sit out 10 minutes) and
the same call is retried on the next key after a short jittered backoff, up to `PROVIDER_KEY_MAX_ATTEMPTS`
keys (default 3). If every key is cooling, the one that is ready soonest still gets the call. This stays
inside one provider and model, so it needs no opt-in; switching providers still only happens with
`"fallback": true`. Each key can carry its own base URL, so Azure deployments in different regions can share
one provider. The response's `meta.attempts` lists every key tried (by its last four characters).

## Anthropic and Responses endpoints

`/v1/messages` takes Anthropic Messages requests (point the Anthropic SDK's base URL at Spendmux and pass the Spendmux key in `x-api-key` or `Authorization`). `/v1/responses` takes OpenAI Responses requests. Both run through the same pipeline as `/v1/chat/completions` (budgets, routing, fallbacks, key pools, memory, logging) and can reach any provider: a bare model goes to Anthropic on `/v1/messages` and OpenAI on `/v1/responses`, and `provider/model` or `X-Spendmux-Provider` picks another. Text, images, tools, tool results and streaming are translated both ways.

Limits: `/v1/responses` is stateless (no `previous_response_id`; use `X-Spendmux-Memory` instead) and accepts function tools only. Thinking blocks are not returned on `/v1/messages`.

## Prompt caching (on by default)

Anthropic bills a cached prompt prefix at a tenth of the input price, but only caches what a request marks with `cache_control`. When a request to Anthropic carries no marks of its own, Spendmux adds them: at the end of the system prompt (or the last tool), and at the end of the conversation once it has more than one turn, so each turn reads the earlier ones from the cache. Answers are unchanged, and a prompt shorter than the model's minimum cacheable length costs nothing extra. OpenAI and most others cache long prompts automatically.

Marks a client sends itself (`cache_control` on Anthropic blocks via `/v1/messages`, or on OpenAI content parts, messages and tools in the LiteLLM style) are forwarded as-is to Anthropic and OpenRouter and removed for other providers, which reject the field. Spend is counted at the real price: cache reads at the cache-read price, cache writes at the write price. Turn the automatic marks off with `X-Spendmux-Prompt-Cache: off`, or per token ("Automatic caching") on `/keys`.

## Response caching

Send `X-Spendmux-Cache: <seconds>` (up to 30 days), or set a cache TTL on a token on `/keys`, and a repeated request is answered from the cache: no provider call, no cost, and no count against dollar caps, token hard stops or rate limits. `X-Spendmux-Cache: refresh` skips the read and stores the fresh reply, `off` bypasses the cache, and `Cache-Control: no-cache` skips the read. Chat (all three formats, streamed or not) and embeddings are cached; a streamed reply is recorded and can be served to a buffered call and the other way round. The response carries `X-Spendmux-Cache: hit|miss` and hits are logged with `cache_hit` and the cost they saved.

Entries are scoped to the account and keyed by provider, model and the canonical request body. The row id and its AES-256-GCM key are both derived from the server secret and the request, so a cached reply can only be read by sending the same request again. Expired rows are deleted every 10 minutes.

Embeddings are cached for 7 days by default, since the same input always embeds to the same vector; "Automatic caching: off" on a token or `X-Spendmux-Cache: off` on a request turns that off. Chat stays opt-in because a stored answer changes behavior for apps that expect a fresh one.

## OpenTelemetry traces

Set `OTEL_EXPORTER_OTLP_ENDPOINT` (or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`, plus `OTEL_EXPORTER_OTLP_HEADERS` for auth) and Spendmux exports traces over OTLP/HTTP; nothing is sent when it is unset. Each `/v1` request gets a server span that joins the caller's trace when it sends `traceparent`, and each provider call or cache hit gets a child span such as `chat gpt-4o` with GenAI attributes (`gen_ai.provider.name`, `gen_ai.request.model`, `gen_ai.usage.input_tokens`, `gen_ai.usage.output_tokens`) and Spendmux's own (`spendmux.cost_usd`, `spendmux.cache_hit`, `spendmux.token_id`, session and tenant). Prompts and completions are never put on spans. `OTEL_SERVICE_NAME` and `OTEL_TRACES_SAMPLER_ARG` work as usual, and responses carry `X-Spendmux-Trace-Id`.

## Conversation memory (opt-in)

Send `X-Spendmux-Memory: <thread id>` (1-128 of `A-Za-z0-9._:-`) with `/v1/chat/completions` and only the new
messages. Spendmux prepends the thread's stored turns after your system prompt (newest that fit
`MEMORY_CONTEXT_CHARS`), forwards the call to whichever provider or fallback hop serves it, and stores the new turn.
The response header `X-Spendmux-Memory-Messages` says how many stored messages were sent. `DELETE /v1/memory/<thread>`
forgets a thread; idle threads expire after `MEMORY_TTL_DAYS`.

Threads are AES-256-GCM encrypted under a key derived from the raw bearer token, which Spendmux stores only as a
hash, so stored memory can't be read without the token. Rotating or deleting a token discards its memory.
Requests without the header are unchanged and store nothing.

## Production

See the [production runbook](docs/production.md). Summary:

1. Fill `.env` from `.env.example` with unique secrets; `APP_ENV=production` refuses to boot on weak, missing, placeholder, or shared secrets, a non-HTTPS `BASE_URL`, or `SPENDMUX_DEBUG=1`.
2. `docker compose -f docker-compose.production.yml up -d --build`: Traefik with Let's Encrypt, the app (3 read-only, non-root replicas; one is fine for a single box) and PostgreSQL, every image pinned by digest. Add `--profile backup` for encrypted off-site backups.
3. Verify `/health/live`, `/health/ready`, and `/metrics` (bearer `METRICS_TOKEN`).

## License

MIT. See [LICENSE](LICENSE).
