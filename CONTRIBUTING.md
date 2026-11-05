# Contributing

Thanks for helping improve Spendmux.

## Setup

```bash
cp .env.example .env
# Set APP_ENV=development, COOKIE_SECRET and KEY_ENCRYPTION_SECRET (openssl rand -base64 32, two different values)

docker compose -f docker-compose.localhost.yml up -d postgres
make frontend-install frontend
go run ./cmd/server
```

See [README.md](./README.md) for the layout and API notes.

## Before you open a PR

1. Run the checks CI runs: `make fmt-check vet race security vuln`. Set `TEST_DATABASE_URL` to a scratch Postgres database to run the tests that need one.
2. If you change UI under `frontend/`, run `make frontend` to check it builds. Built assets are not committed.
3. Prefer small, focused PRs with a short description of why.

## License

By contributing, you agree your contributions are licensed under the MIT License (see [LICENSE](./LICENSE)).
