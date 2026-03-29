.PHONY: help build install test test-integration lint coverage coverage-check docker-build run clean restart docker-shell docker-snapshot app-dev app-dev-docker app-install app-build-binary app-dist-linux app-icons
.DEFAULT_GOAL := help

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*##' $(MAKEFILE_LIST) | awk -F ':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
APP_VERSION ?= $(shell git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//' || echo 0.0.0)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE        ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

build: ## Build the loop binary
	go generate ./internal/readme/
	go build -ldflags "$(LDFLAGS)" -o bin/loop ./cmd/loop

install: ## Install loop to GOPATH/bin
	go generate ./internal/readme/
	go install -ldflags "$(LDFLAGS)" ./cmd/loop

test: ## Run all tests
	go generate ./internal/readme/
	go test -race -count=1 -timeout 60s ./...

test-integration: ## Run integration tests (requires tokens in ~/.loop/config.integration.json)
	go test -v -tags integration -race -count=1 -timeout 10m ./internal/slack/ ./internal/discord/

lint: ## Run golangci-lint (with auto-fix)
	docker run --rm --name loop-lint -v "$$(pwd)":/app -v /app/app/node_modules -w /app golangci/golangci-lint:v2.11.4 golangci-lint run -v --fix ./...

coverage: ## Generate HTML coverage report
	go generate ./internal/readme/
	go test -race -count=1 -coverpkg=./... -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

coverage-check: ## Run tests and enforce 100% coverage
	go generate ./internal/readme/
	go test -race -count=1 -timeout 60s -coverpkg=./... -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out 2>/dev/null | grep total | awk '{print $$3}' | sed 's/%//' | \
		awk '{if ($$1 < 100.0) {print "Coverage is " $$1 "%, required 100%"; exit 1} else {print "Coverage: " $$1 "%"}}'

CLAUDE_VERSION := $(shell curl -sf https://storage.googleapis.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases/latest 2>/dev/null || echo latest)

docker-build: ## Build the Docker container images (agent + chrome)
	docker build --build-arg CLAUDE_VERSION=$(CLAUDE_VERSION) --secret id=gitconfig,src=$(HOME)/.gitconfig -t loop-agent -f container/Dockerfile .
	docker build -t loop-chrome -f internal/container/image/chrome.Dockerfile internal/container/image/

run: build ## Build and run the bot
	./bin/loop serve

restart: install docker-build ## Install, stop and start the daemon
	@echo "Claude CLI version: $(CLAUDE_VERSION)"
	$(shell go env GOPATH)/bin/loop daemon:stop || true
	#docker volume rm -f loop-npmcache loop-uvcache loop-cache loop-gocache
	$(shell go env GOPATH)/bin/loop daemon:start

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

# --- App build targets ---

# Build Go binary for a specific GOOS/GOARCH into app/resources/{ebOS}/{ebArch}/
# Maps Go naming to electron-builder naming: darwin→mac, linux→linux, amd64→x64
# Usage: make app-build-binary GOOS=linux GOARCH=amd64
app-build-binary: ## Cross-compile loop binary for app bundling (GOOS=, GOARCH=)
	go generate ./internal/readme/
	@EB_OS="$(GOOS)"; \
	if [ "$(GOOS)" = "darwin" ]; then EB_OS="mac"; fi; \
	EB_ARCH="$(GOARCH)"; \
	if [ "$(GOARCH)" = "amd64" ]; then EB_ARCH="x64"; fi; \
	mkdir -p app/resources/$$EB_OS/$$EB_ARCH; \
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" \
		-o app/resources/$$EB_OS/$$EB_ARCH/loop ./cmd/loop; \
	echo "Built app/resources/$$EB_OS/$$EB_ARCH/"

app-dev: ## Start the Electron app frontend dev server
	cd app && npm install && npx vite --host

app-dev-docker: ## Start Vite frontend dev server in Docker (no Electron, browser-accessible)
	docker run --rm -it --name loop-app-dev \
		-v "$$(pwd)/app":/app -w /app \
		-p 5173:5173 \
		--add-host=host.docker.internal:host-gateway \
		node:24-alpine sh -c "npm install && npx vite --host 0.0.0.0 --config vite.browser.config.ts"

app-install: build ## Build the Electron app and copy to /Applications
	@mkdir -p app/resources/mac/arm64
	cp bin/loop app/resources/mac/arm64/loop
	cd app && npm install && npm pkg set version=$(APP_VERSION) && npm run dist:mac:arm64
	rm -rf /Applications/Loop.app
	cp -R app/release/mac-arm64/Loop.app /Applications/Loop.app
	@echo "Installed Loop.app to /Applications"

app-dist-linux: ## Build Linux AppImage + deb (x64 and arm64)
	$(MAKE) app-build-binary GOOS=linux GOARCH=amd64
	$(MAKE) app-build-binary GOOS=linux GOARCH=arm64
	cd app && npm install && npm pkg set version=$(APP_VERSION) && npm run dist:linux

app-icons: ## Regenerate app icons from SVG sources (requires rsvg-convert, iconutil, fontkit)
	@npm list --prefix /tmp fontkit >/dev/null 2>&1 || npm install --prefix /tmp fontkit
	@node scripts/build-icons.js
	@mkdir -p /tmp/Loop.iconset
	@for size in 16 32 64 128 256 512; do \
		rsvg-convert -w $$size -h $$size app/build/icon.svg -o /tmp/Loop.iconset/icon_$${size}x$${size}.png; \
	done
	@rsvg-convert -w 1024 -h 1024 app/build/icon.svg -o /tmp/Loop.iconset/icon_512x512@2x.png
	@cp /tmp/Loop.iconset/icon_32x32.png /tmp/Loop.iconset/icon_16x16@2x.png
	@cp /tmp/Loop.iconset/icon_64x64.png /tmp/Loop.iconset/icon_32x32@2x.png
	@cp /tmp/Loop.iconset/icon_256x256.png /tmp/Loop.iconset/icon_128x128@2x.png
	@cp /tmp/Loop.iconset/icon_512x512.png /tmp/Loop.iconset/icon_256x256@2x.png
	@rm /tmp/Loop.iconset/icon_64x64.png
	@iconutil -c icns /tmp/Loop.iconset -o app/build/icon.icns
	@rm -rf /tmp/Loop.iconset
	@rsvg-convert -w 512 -h 512 app/build/icon.svg -o app/public/loop-macos.png
	@rsvg-convert -w 512 -h 512 app/build/icon-transparent.svg -o app/public/loop.png
	@echo "Generated: app/build/icon.icns, app/public/loop-macos.png, app/public/loop.png"

clean: ## Remove build artifacts
	rm -rf bin/ app/resources/ coverage.out coverage.html
