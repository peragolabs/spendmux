# Base images are pinned by digest: a re-pointed tag upstream cannot change
# what gets built. Bump them deliberately (docker buildx imagetools inspect).

# ── Stage 1: React SPA → priv/www ────────────────────────────
FROM node:22.23.4-alpine@sha256:fe4f6d74e20370acb04ad889077d14d0f64a9c8ea6a42bc60cddf1e844699914 AS frontend
WORKDIR /src
COPY frontend/react/package.json frontend/react/package-lock.json ./frontend/react/
RUN cd frontend/react && npm ci
COPY frontend/react ./frontend/react
COPY priv ./priv
RUN rm -rf priv/www/assets && cd frontend/react && npm run build

# ── Stage 2: Go binary ───────────────────────────────────────
FROM golang:1.26.9-alpine@sha256:3082400e369fa24d5fc60bca20edab3f6d604e0c5a690ec66b295eff4dd87ade AS builder
RUN apk add --no-cache ca-certificates git
# go.mod may require a newer patch release than this image ships (security
# fixes land before the image does); Go then fetches that toolchain and checks
# it against the public checksum database.
ENV GOTOOLCHAIN=auto
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY middleware ./middleware
COPY models ./models
COPY oss ./oss
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/spendmux ./cmd/server

# ── Stage 3: runtime (non-root, read-only friendly) ──────────
# Fixed uid/gid 10001 so the host can grant it read access to .env (see compose).
FROM alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
RUN apk add --no-cache ca-certificates tzdata wget \
    && addgroup -S -g 10001 spendmux \
    && adduser -S -u 10001 -G spendmux -h /app spendmux
WORKDIR /app
COPY --from=builder  --chown=spendmux:spendmux /out/spendmux ./spendmux
COPY --from=frontend --chown=spendmux:spendmux /src/priv ./priv
ENV PORT=8080
EXPOSE 8080
USER spendmux
HEALTHCHECK --interval=15s --timeout=5s --start-period=20s --retries=5 \
  CMD wget -qO- http://127.0.0.1:8080/health/ready || exit 1
ENTRYPOINT ["./spendmux"]
