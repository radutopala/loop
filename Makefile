.PHONY: help build install test lint coverage coverage-check docker-build run clean restart
.DEFAULT_GOAL := help

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*##' $(MAKEFILE_LIST) | awk -F ':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

build: ## Build the loop binary
	go build -o bin/loop ./cmd/loop

install: ## Install loop to GOPATH/bin
	go install ./cmd/loop

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

restart: install ## Install, stop and start the daemon
	loop daemon stop || true
	loop daemon start

clean: ## Remove build artifacts
	rm -rf bin/ coverage.out coverage.html
