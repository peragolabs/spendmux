# Spendmux production runbook

## Before the first deployment

0. On a fresh Ubuntu or Debian server, `sudo sh scripts/setup-server.sh` installs or updates Docker, creates `/opt/apps`, and makes a read-only GitHub deploy key. Open ports 80 and 443 in the firewall.
1. Copy `.env.example` to `.env` next to the Compose file. Never commit it. The app container mounts it read-only and loads it itself, so secrets never enter its process environment or `docker inspect`; restrict it to you and the container user: `sudo chown <you>:10001 .env && chmod 640 .env`. Make `vault.pem` readable by the same group (`sudo chown root:10001 vault.pem && chmod 640 vault.pem`).
2. Generate independent values for `COOKIE_SECRET`, `KEY_ENCRYPTION_SECRET`, `METRICS_TOKEN`, and `POSTGRES_PASSWORD`. Production startup rejects weak, missing, or shared cookie/encryption secrets.
3. Generate the vault keypair (`openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:4096 -out vault.pem`), keep it with the same care as an SSH host key, and set `VAULT_PRIVATE_KEY_PATH` for Compose (mounted read-only at `/run/secrets/vault.pem`). Browsers seal provider keys with its public half; without the private key every sealed key is unrecoverable. Back it up with `KEY_ENCRYPTION_SECRET` — recovering a key needs both.
4. Set an HTTPS `BASE_URL`, a URL-encoded `DATABASE_URL`, and at least one complete OAuth provider.
5. Set `SPENDMUX_HOST` and `ACME_EMAIL`. Traefik ships in `docker-compose.production.yml`: it takes ports 80 and 443, gets Let's Encrypt certificates over the HTTP challenge, and reads containers through a read-only Docker socket proxy. Every image is pinned by digest. Optionally set `SPENDMUX_API_HOST` to serve the API on its own hostname, and `SPENDMUX_API_URL` to the `base_url` your pages should show.

Set `SPENDMUX_ALLOW_PRIVATE_UPSTREAMS=true` only when everyone who can sign in is trusted. It disables the proxy's SSRF boundary so local providers such as Ollama can be reached.

## Deploy and verify

```sh
docker compose -f docker-compose.production.yml config
docker compose -f docker-compose.production.yml build
docker compose -f docker-compose.production.yml up -d
curl -fsS https://spendmux.example/health/live
curl -fsS https://spendmux.example/health/ready
curl -fsS -H "Authorization: Bearer $METRICS_TOKEN" https://spendmux.example/metrics
```

Startup migrations run in a PostgreSQL transaction protected by an advisory lock, so replicas cannot migrate concurrently. A migration failure prevents the process from serving traffic. Take a database backup before every schema-changing release.

Sign-in state and sessions live in PostgreSQL, so any replica can finish a login and no session affinity is needed.

The production Compose file also applies an edge request-rate limit. The in-process browser limiter is disabled in production because it would see the reverse proxy as one client; keep an equivalent trusted-edge limit when using another ingress. `/v1` additionally has its PostgreSQL-backed per-platform-key limit.

## Behind Cloudflare (optional)

Traefik trusts forwarding headers only from Cloudflare's IP ranges (listed in the Compose file), so the client IP cannot be forged; without Cloudflare it uses the connecting address. If you proxy through Cloudflare, use SSL mode Full (strict), add a Transform Rule that sets `X-Real-IP` to `ip.src`, and keep the API hostname DNS only: Cloudflare's proxy ends requests after 100 seconds, which long model calls can exceed.

## Backups and restore drills

The optional `backup` service dumps the database daily at `BACKUP_AT` (UTC), encrypts it with [age](https://age-encryption.org) to `BACKUP_AGE_RECIPIENT`, and uploads it to `BACKUP_REMOTE` with rclone (Backblaze B2 via `B2_KEY_ID` / `B2_APPLICATION_KEY`). Generate the age key with `age-keygen` and keep the private half off the server. Turn it on with the `backup` profile:

```sh
docker compose -f docker-compose.production.yml --profile backup up -d
docker compose -f docker-compose.production.yml run --rm backup backup.sh now   # one-off
```

- Run encrypted daily `pg_dump --format=custom` backups and retain at least 30 days.
- Store backups outside the application host with restricted credentials and lifecycle protection.
- Test a restore into an isolated database at least monthly with `pg_restore --clean --if-exists`.
- Verify restored row counts for users, provider keys, platform keys, fallback chains, and usage events.
- Verify decryption with the backed-up `KEY_ENCRYPTION_SECRET`; losing it makes provider keys unrecoverable.

## Rotation and incident response

- Rotate `METRICS_TOKEN`, OAuth secrets, and database credentials through the secret manager, then roll the replicas.
- Treat `COOKIE_SECRET` rotation as a forced sign-out.
- Do not rotate `KEY_ENCRYPTION_SECRET` without a dual-key re-encryption migration.
- Rotating the vault keypair requires re-wrapping every sealed envelope with the new public key (needs the old private key once); until then users can re-save keys from `/keys`, which seals them with the current key. The public key id (`kid`) is checked on save so a stale page cannot seal with a retired key.
- Revoke leaked platform keys in `/keys`. Rate limits and token budgets are enforced in PostgreSQL across replicas.
- Rotate leaked provider keys at the provider and replace their encrypted vault entries.
- Keep `/api/debug/*` disabled. Those routes exist only in development or with `SPENDMUX_DEBUG=1`.

## Monitoring and release gates

- Alert on readiness failures, HTTP 5xx increases, repeated upstream circuit failures, and budget/rate-limit service errors.
- Scrape `/metrics` using its bearer token, preferably over a private network.
- Before release, run `go test -race ./...`, `go vet ./...`, `govulncheck ./...`, `make security`, `npm audit`, and a container build. GitHub Actions enforces these gates.
- Review provider price tables before releases; model pricing changes independently of this code.
