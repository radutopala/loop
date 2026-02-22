.PHONY: help build install test test-integration lint coverage coverage-check docker-build run clean restart docker-shell docker-snapshot
.DEFAULT_GOAL := help

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*##' $(MAKEFILE_LIST) | awk -F ':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

build: ## Build the loop binary
	go build -ldflags "$(LDFLAGS)" -o bin/loop ./cmd/loop

install: ## Install loop to GOPATH/bin
	go install -ldflags "$(LDFLAGS)" ./cmd/loop

test: ## Run all tests
	go test -race -count=1 ./...

test-integration: ## Run integration tests (requires tokens in ~/.loop/config.integration.json)
	go test -v -tags integration -race -count=1 -timeout 10m ./internal/slack/ ./internal/discord/

lint: ## Run golangci-lint
	docker run --rm --name loop-lint -v "$$(pwd)":/app -w /app golangci/golangci-lint:v2.10.1 golangci-lint run -v ./...

coverage: ## Generate HTML coverage report
	go test -race -count=1 -coverprofile=coverage.out ./...
	@sed '/^$$/d' coverage.out > coverage.out.tmp && mv coverage.out.tmp coverage.out
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

coverage-check: ## Run tests and enforce 100% coverage
	go test -race -count=1 -coverprofile=coverage.out ./...
	@sed '/^$$/d' coverage.out > coverage.out.tmp && mv coverage.out.tmp coverage.out
	@go tool cover -func=coverage.out | grep total | awk '{print $$3}' | sed 's/%//' | \
		awk '{if ($$1 < 100.0) {print "Coverage is " $$1 "%, required 100%"; exit 1} else {print "Coverage: " $$1 "%"}}'

CLAUDE_VERSION := $(shell curl -sf https://storage.googleapis.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases/latest 2>/dev/null || echo latest)

docker-build: ## Build the Docker container image
	docker build --build-arg CLAUDE_VERSION=$(CLAUDE_VERSION) --secret id=gitconfig,src=$(HOME)/.gitconfig -t loop-agent -f container/Dockerfile .

run: build ## Build and run the bot
	./bin/loop serve

restart: install docker-build ## Install, stop and start the daemon
	@echo "Claude CLI version: $(CLAUDE_VERSION)"
	loop daemon:stop || true
	#docker volume rm -f loop-npmcache loop-uvcache loop-cache loop-gocache
	loop daemon:start

docker-shell: ## Start a bash shell in the agent container (requires make docker-snapshot first)
	docker run --rm -it $$(cat ~/.loop/snapshot-run) loop-agent:snapshot bash

docker-snapshot: ## Snapshot the most recent loop-agent container into loop-agent:snapshot
	@CID=$$(docker ps -aq --filter label=app=loop-agent | head -1); \
	if [ -z "$$CID" ]; then echo "No loop-agent container found"; exit 1; fi; \
	echo "Committing container $$CID to loop-agent:snapshot"; \
	docker commit "$$CID" loop-agent:snapshot; \
	VOLS=$$(docker inspect --format '{{range .Mounts}}{{if eq .Type "volume"}}-v {{.Name}}:{{.Destination}} {{else if eq .Type "bind"}}-v {{.Source}}:{{.Destination}}{{if .Mode}}:{{.Mode}}{{end}} {{end}}{{end}}' "$$CID"); \
	ENVS=$$(docker inspect --format '{{range .Config.Env}}-e {{.}} {{end}}' "$$CID"); \
	WORKDIR=$$(docker inspect --format '{{.Config.WorkingDir}}' "$$CID"); \
	echo "$$VOLS $$ENVS -w $$WORKDIR --add-host=host.docker.internal:host-gateway" > ~/.loop/snapshot-run; \
	echo 'Run with: make docker-shell'

clean: ## Remove build artifacts
	rm -rf bin/ coverage.out coverage.html
