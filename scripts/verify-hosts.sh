#!/bin/sh
# verify-hosts.sh HOST [HOST...]
#
# Read-only check that each hostname serves Spendmux over HTTPS. Changes
# nothing and never calls a provider, so it costs nothing to run.
#
# Per host it checks: DNS resolves, the TLS certificate is valid, /health/ready
# answers 200, and /v1/models answers Spendmux's own 401 auth_error (which
# proves the request reached the proxy, not a Traefik 404 or another site).
# With SPENDMUX_TEST_TOKEN set to an app token, /v1/models must answer 200.
#
#   scripts/verify-hosts.sh spendmux.example api.spendmux.example
set -eu

[ "$#" -gt 0 ] || { echo "usage: $0 HOST [HOST...]" >&2; exit 2; }

failed=0
pass() { printf '  ok    %s\n' "$1"; }
fail() { printf '  FAIL  %s\n' "$1"; failed=1; }

for host in "$@"; do
	echo "$host"
	if getent hosts "$host" >/dev/null 2>&1 || nslookup "$host" >/dev/null 2>&1; then pass "DNS resolves"; else fail "DNS does not resolve"; continue; fi

	code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 15 "https://$host/health/ready" 2>/dev/null) || code=tls
	case "$code" in
		200) pass "HTTPS + /health/ready" ;;
		tls) fail "HTTPS failed (certificate not issued yet, or host not routed)" ;;
		*) fail "/health/ready answered $code" ;;
	esac

	body=$(curl -sS --max-time 15 "https://$host/v1/models" 2>/dev/null || true)
	case "$body" in
		*auth_error*) pass "/v1 reaches the Spendmux proxy" ;;
		*) fail "/v1/models did not answer Spendmux's auth_error" ;;
	esac

	if [ -n "${SPENDMUX_TEST_TOKEN:-}" ]; then
		code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 15 -H "Authorization: Bearer $SPENDMUX_TEST_TOKEN" "https://$host/v1/models" 2>/dev/null || echo error)
		[ "$code" = 200 ] && pass "/v1/models with token" || fail "/v1/models with token answered $code"
	fi
done

[ "$failed" = 0 ] && echo "all hosts ok" || { echo "some checks failed" >&2; exit 1; }
