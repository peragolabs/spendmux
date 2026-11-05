.PHONY: all build frontend frontend-install run test race vet fmt fmt-check security vuln docker-up docker-down clean

all: build

build: frontend
	go build -trimpath -o spendmux ./cmd/server

frontend-install:
	cd frontend/react && npm ci

frontend:
	cd frontend/react && npm run build

run: build
	./spendmux

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './frontend/*')

fmt-check:
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './frontend/*'))" || \
		(echo "Go files need formatting"; gofmt -l $$(find . -name '*.go' -not -path './frontend/*'); exit 1)

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...

security:
	go run github.com/securego/gosec/v2/cmd/gosec@v2.29.0 -quiet ./...

docker-up:
	docker compose -f docker-compose.localhost.yml up -d --build

docker-down:
	docker compose -f docker-compose.localhost.yml down

clean:
	rm -f spendmux coverage.out
	rm -rf priv/www/assets frontend/react/dist
