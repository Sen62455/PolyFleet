VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X github.com/Sen62455/PolyFleet/internal/buildinfo.Version=$(VERSION) \
	-X github.com/Sen62455/PolyFleet/internal/buildinfo.Commit=$(COMMIT) \
	-X github.com/Sen62455/PolyFleet/internal/buildinfo.Date=$(BUILD_DATE)

.PHONY: test web-test web build release clean

test:
	go test ./...
	cd web && pnpm typecheck && pnpm test

web-test:
	cd web && pnpm typecheck && pnpm test

web:
	cd web && pnpm install --frozen-lockfile && pnpm build

build: web
	mkdir -p bin
	CGO_ENABLED=0 go build -tags webui -ldflags "$(LDFLAGS)" -o bin/hyfleet-server ./cmd/server
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/hyfleet-agent ./cmd/agent
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/hyfleet-agent-ops ./cmd/agentops

release: web
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags webui -ldflags "$(LDFLAGS)" -o bin/hyfleet-server-linux-amd64 ./cmd/server
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/hyfleet-agent-linux-amd64 ./cmd/agent
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/hyfleet-agent-ops-linux-amd64 ./cmd/agentops
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags webui -ldflags "$(LDFLAGS)" -o bin/hyfleet-server-linux-arm64 ./cmd/server
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/hyfleet-agent-linux-arm64 ./cmd/agent
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/hyfleet-agent-ops-linux-arm64 ./cmd/agentops
	cd bin && sha256sum hyfleet-*-linux-* > SHA256SUMS

clean:
	rm -rf bin internal/webui/dist
