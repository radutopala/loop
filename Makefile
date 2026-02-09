.PHONY: help build install test lint coverage coverage-check docker-build run clean restart docker-shell
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

lint: ## Run golangci-lint
	golangci-lint run ./...

coverage: ## Generate HTML coverage report
	go test -race -count=1 -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

coverage-check: ## Run tests and enforce 100% coverage
	go test -race -count=1 -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | grep total | awk '{print $$3}' | sed 's/%//' | \
		awk '{if ($$1 < 100.0) {print "Coverage is " $$1 "%, required 100%"; exit 1} else {print "Coverage: " $$1 "%"}}'

docker-build: ## Build the Docker container image
	docker build -t loop-agent -f container/Dockerfile .

run: build ## Build and run the bot
	./bin/loop serve

restart: install docker-build ## Install, stop and start the daemon
	loop daemon:stop || true
	loop daemon:start

docker-shell: ## Start a bash shell in the agent container
	docker run --rm -it --entrypoint bash \
		--add-host=host.docker.internal:host-gateway \
		-v $(CURDIR):$(CURDIR) \
		-v $(HOME)/.claude:/home/agent/.claude \
		-v $(HOME)/.gitconfig:/home/agent/.gitconfig:ro \
		-v $(HOME)/.gitignore_global:$(HOME)/.gitignore_global:ro \
		-v $(HOME)/.ssh:/home/agent/.ssh:ro \
		-v $(HOME)/.aws:/home/agent/.aws \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-w $(CURDIR) \
		-e CLAUDE_CODE_OAUTH_TOKEN=$$(grep claude_code_oauth_token ~/.loop/config.json | awk -F'"' '{print $$4}') \
		loop-agent:latest

clean: ## Remove build artifacts
	rm -rf bin/ coverage.out coverage.html
