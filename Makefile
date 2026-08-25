.PHONY: help build install test test-integration test-component test-runner-build test-runner-push bdd-serve lint lint-go lint-app coverage coverage-check codeql-download codeql docker-build docs-build docs-serve docs-capture run clean restart docker-shell docker-snapshot app-dev app-dev-docker app-test app-install app-build-binary app-dist-linux app-icons _sync-loop-overrides
.DEFAULT_GOAL := help

# Strip gate-child env inheritance when invoking make from inside a
# loop-syscallwrap'd shell: those vars tell test code it's running as the
# seccomp-gate child process (fd 3 is the live handshake socket, etc.) and
# cause child_test.go:TestDefaultParentConnHappyOnSocketpair to skip, which
# drops coverage below 100%. Tests need the clean-shell environment.
unexport LOOP_SYSCALLWRAP_MODE
unexport LOOP_GATE_ENABLED

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
	go test -race -count=1 -timeout 90s ./...

test-integration: ## Run integration tests (requires tokens in ~/.loop/config.integration.json)
	go test -v -tags integration -race -count=1 -timeout 10m ./internal/slack/ ./internal/discord/

# Documentation-capture scenarios are tagged @docs and excluded by default so
# normal runs (and CI) stay fast and don't write assets. `make docs-capture`
# overrides this to run only @docs scenarios with capture enabled.
GODOG_TAGS ?= ~@docs && ~@journey

test-component-bdd: ## Run BDD component tests (via Docker on host, natively in CI)
	@if { [ "$$CI" = "true" ] || ([ -f /.dockerenv ] && [ "$$(id -u)" = "0" ] && command -v apt-get >/dev/null 2>&1); } && [ -z "$(LOOP_DOCS_CAPTURE)" ]; then \
		GODOG_TAGS="$(GODOG_TAGS)" LOOP_DOCS_CAPTURE="$(LOOP_DOCS_CAPTURE)" $(if $(LOOP_DOCS_CAPTURE),LOOP_DOCS_HOST_CONFIG="$(HOME)/.loop/config.json") TEST_RUN=$${TEST_RUN:-"TestBDDBackendFeatures|TestBDDFrontendFeatures"} bash scripts/test-component.sh; \
	else \
		docker rm -f loop-bdd 2>/dev/null; \
		rm -rf /tmp/loop-bdd-data && mkdir -p /tmp/loop-bdd-data; \
		docker run --name loop-bdd -v "$$(pwd)":/app -w /app \
			-v /var/run/docker.sock:/var/run/docker.sock \
			-v /tmp/loop-bdd-data:/tmp/loop-bdd-data \
			-v loop-gomod:/go/pkg/mod -v loop-gocache:/root/.cache/go-build \
			-e TEST_RUN="$${TEST_RUN:-TestBDDBackendFeatures|TestBDDFrontendFeatures}" \
			$(if $(GODOG_TAGS),-e GODOG_TAGS="$(GODOG_TAGS)") \
			$(if $(LOOP_DOCS_CAPTURE),-e LOOP_DOCS_CAPTURE="$(LOOP_DOCS_CAPTURE)" -v "$(HOME)/.loop/config.json:/host-loop-config.json:ro" -e LOOP_DOCS_HOST_CONFIG=/host-loop-config.json) \
			$(if $(GODOG_CONCURRENCY),-e GODOG_CONCURRENCY="$(GODOG_CONCURRENCY)") \
			ghcr.io/radutopala/loop/test-runner:latest bash scripts/test-component.sh; \
		rc=$$?; docker ps -aq --filter "name=loop-bdd-" | xargs -r docker rm -f 2>/dev/null || true; exit $$rc; \
	fi

bdd-serve: ## Build + run the daemon and UI inside Docker as a STANDING instance (no tests), with live agents, for manual / MCP-browser testing. Prints the bridge URL to connect to. Stop with: docker rm -f loop-dev
	@docker rm -f loop-dev 2>/dev/null || true; \
	rm -rf /tmp/loop-bdd-data && mkdir -p /tmp/loop-bdd-data; \
	echo "Building + starting loop in Docker (container: loop-dev)..."; \
	docker run -d --name loop-dev -v "$$(pwd)":/app -w /app \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-v /tmp/loop-bdd-data:/tmp/loop-bdd-data \
		-v loop-gomod:/go/pkg/mod -v loop-gocache:/root/.cache/go-build \
		-v "$(HOME)/.loop/config.json:/host-loop-config.json:ro" \
		-e LOOP_SERVE_ONLY=1 -e LOOP_DOCS_CAPTURE=1 -e LOOP_DOCS_HOST_CONFIG=/host-loop-config.json \
		ghcr.io/radutopala/loop/test-runner:latest bash scripts/test-component.sh >/dev/null; \
	for i in $$(seq 1 90); do \
		ip=$$(docker inspect loop-dev --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' 2>/dev/null); \
		if [ -n "$$ip" ] && docker exec loop-dev curl -sf http://localhost:8222/api/health >/dev/null 2>&1; then \
			echo ""; echo "loop-dev is up — connect a browser (e.g. MCP) to:"; \
			echo "   UI:  http://$$ip:5173"; \
			echo "   API: http://$$ip:8222"; \
			echo "Logs: docker logs -f loop-dev    Stop: docker rm -f loop-dev"; \
			exit 0; \
		fi; \
		if [ "$$(docker inspect loop-dev --format '{{.State.Running}}' 2>/dev/null)" != "true" ]; then \
			echo "loop-dev exited early:"; docker logs loop-dev 2>&1 | tail -20; exit 1; \
		fi; \
		sleep 3; \
	done; \
	echo "loop-dev did not become healthy in time; see: docker logs loop-dev"; exit 1

docs-capture: ## Capture documentation screenshots/GIFs from @docs BDD scenarios (incl. a live Claude agent chat; reuses one sample project) into docs/static/images/features
	$(MAKE) test-component-bdd GODOG_TAGS=@docs LOOP_DOCS_CAPTURE=1 TEST_RUN=TestBDDFrontendFeatures

docs-capture-section: ## Capture+record a single docs section for fast iteration, e.g. SECTION=git or SECTION=browser (tags: intro chat gate shortcuts git editor memory terminal git-panel browser sessions swarm canvas playground kanban workflows-tab quality multi-panel worktrees tasks workflows-panel settings outro)
	@test -n "$(SECTION)" || { echo "Usage: make docs-capture-section SECTION=<name>"; exit 1; }
	$(MAKE) test-component-bdd GODOG_TAGS=@docs-$(SECTION) LOOP_DOCS_CAPTURE=1 TEST_RUN=TestBDDFrontendFeatures

docs-journey: ## Record the WHOLE walkthrough as one continuous take (single browser session, one start/stop) into docs/videos/journey.mp4 with a continuous soundtrack — no per-section stitching. Regenerates journey.feature from docs_capture.feature first.
	go run scripts/gen-journey-feature.go test/component/features/frontend/docs_capture.feature test/component/features/frontend/journey.feature
	$(MAKE) test-component-bdd GODOG_TAGS=@journey LOOP_DOCS_CAPTURE=1 TEST_RUN=TestBDDFrontendFeatures

test-component-perf: ## Run API performance tests (via Docker on host, natively in CI)
	@if [ "$$CI" = "true" ] || ([ -f /.dockerenv ] && [ "$$(id -u)" = "0" ] && command -v apt-get >/dev/null 2>&1); then \
		TEST_RUN=TestAPIPerfTestSuite bash scripts/test-component.sh; \
	else \
		docker run --rm -v "$$(pwd)":/app -w /app \
			-v loop-gomod:/go/pkg/mod -v loop-gocache:/root/.cache/go-build \
			-e TEST_RUN=TestAPIPerfTestSuite \
			ghcr.io/radutopala/loop/test-runner:latest bash scripts/test-component.sh; \
	fi

test-component-bdd-host: ## Run frontend BDD tests against host Chrome browser (no Docker)
	CHROME_CDP_URL=$${CHROME_CDP_URL:-auto} GODOG_TAGS="$(GODOG_TAGS)" LOOP_DOCS_CAPTURE="$(LOOP_DOCS_CAPTURE)" TEST_RUN=$${TEST_RUN:-TestBDDFrontendFeatures} GODOG_CONCURRENCY=1 bash scripts/test-component.sh

TEST_RUNNER_IMAGE := ghcr.io/radutopala/loop/test-runner:latest

test-runner-build: ## Build the test-runner Docker image
	docker build -t $(TEST_RUNNER_IMAGE) -f scripts/test-runner.Dockerfile scripts/

test-runner-push: test-runner-build ## Build and push the test-runner Docker image
	docker push $(TEST_RUNNER_IMAGE)

lint: lint-go lint-app ## Run golangci-lint + biome/tsc (with auto-fix)

lint-go: ## Run golangci-lint (with auto-fix)
	@if [ -n "$$(docker ps --filter name=^loop-lint$$ --quiet)" ]; then \
		echo "error: another loop-lint container is already running; aborting" >&2; \
		exit 1; \
	fi
	docker run --rm --name loop-lint -v "$$(pwd)":/app -v /app/app/node_modules -w /app golangci/golangci-lint:v2.13.1 golangci-lint run -v --fix ./...

lint-app: ## Run biome (with auto-fix) + tsc typecheck on the app
	@if [ -n "$$(docker ps --filter name=^loop-lint-biome$$ --quiet)" ]; then \
		echo "error: another loop-lint-biome container is already running; aborting" >&2; \
		exit 1; \
	fi
	docker run --rm --name loop-lint-biome \
		-v "$$(pwd)/app":/app -w /app \
		-v loop-npmcache:/root/.npm \
		node:24-alpine sh -c "npm install && npm run format && npm run typecheck"

coverage: ## Generate HTML coverage report
	go generate ./internal/readme/
	go test -race -count=1 -coverpkg=./... -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

_coverage-check-run:
	go generate ./internal/readme/
	go test -race -count=1 -timeout 90s -coverpkg=./... -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out 2>/dev/null | grep total | awk '{print $$3}' | sed 's/%//' | \
		awk '{if ($$1 < 100.0) {print "Coverage is " $$1 "%, required 100%"; exit 1} else {print "Coverage: " $$1 "%"}}'

coverage-check: ## Run tests and enforce 100% coverage (via Docker on host, directly in CI)
	@if [ "$$CI" = "true" ] || [ -f /.dockerenv ]; then \
		$(MAKE) _coverage-check-run; \
	else \
		docker run --rm -v "$$(pwd)":/app -w /app golang:1.27 make _coverage-check-run; \
	fi

# Hugo version pinned to match .github/workflows/pages.yaml so local
# builds reproduce CI output exactly. Update both when bumping.
HUGO_VERSION ?= 0.154.5
DOCS_BASE_URL ?= https://radutopala.github.io/loop/
DOCS_SERVE_PORT ?= 8080

# On macOS, the host's TLS trust store (including any corporate CAs the
# user has installed via Keychain Access) is extracted into a temp PEM
# and mounted into the container so `hugo mod get` / `git ls-remote`
# survive transparent MITM inspection (e.g. Palo Alto Prisma). On
# Linux/CI, the container's own bundle is used unchanged.
docs-build: ## Build the docs/ Hugo site via Docker (output in docs/public/)
	@if [ -n "$$(docker ps --filter name=^loop-docs$$ --quiet)" ]; then \
		echo "error: another loop-docs container is already running; aborting" >&2; \
		exit 1; \
	fi
	cp docs/README.md docs/_index.md
	@set -e; \
	CA_BUNDLE=""; CA_BUNDLE_TMP=""; \
	if [ -n "$(DOCS_CA_BUNDLE)" ]; then \
		CA_BUNDLE="$(DOCS_CA_BUNDLE)"; \
	elif [ "$$(uname)" = "Darwin" ]; then \
		CA_BUNDLE_TMP="$$(mktemp -t loop-docs-ca)"; \
		security find-certificate -a -p /Library/Keychains/System.keychain >> "$$CA_BUNDLE_TMP" 2>/dev/null || true; \
		security find-certificate -a -p /System/Library/Keychains/SystemRootCertificates.keychain >> "$$CA_BUNDLE_TMP" 2>/dev/null || true; \
		if [ -s "$$CA_BUNDLE_TMP" ]; then CA_BUNDLE="$$CA_BUNDLE_TMP"; else rm -f "$$CA_BUNDLE_TMP"; CA_BUNDLE_TMP=""; fi; \
	fi; \
	trap '[ -n "$$CA_BUNDLE_TMP" ] && rm -f "$$CA_BUNDLE_TMP"' EXIT; \
	if [ -n "$$CA_BUNDLE" ]; then \
		docker run --rm --name loop-docs -v "$$(pwd)":/repo -w /repo/docs \
			-v "$$CA_BUNDLE":/usr/local/share/ca-certificates/host-ca.crt:ro \
			hugomods/hugo:exts-$(HUGO_VERSION) \
			sh -c 'cat /usr/local/share/ca-certificates/host-ca.crt >> /etc/ssl/certs/ca-certificates.crt; git config --global --add safe.directory /repo; git config --global http.sslCAInfo /etc/ssl/certs/ca-certificates.crt; export SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt; hugo mod get -u && hugo --gc --minify --baseURL $(DOCS_BASE_URL)'; \
	else \
		docker run --rm --name loop-docs -v "$$(pwd)":/repo -w /repo/docs \
			hugomods/hugo:exts-$(HUGO_VERSION) \
			sh -c 'git config --global --add safe.directory /repo; hugo mod get -u && hugo --gc --minify --baseURL $(DOCS_BASE_URL)'; \
	fi
	@echo "Built site in docs/public/"

docs-serve: ## Build docs (baseURL=localhost) and serve at http://localhost:$(DOCS_SERVE_PORT)/
	@$(MAKE) --no-print-directory docs-build DOCS_BASE_URL=http://localhost:$(DOCS_SERVE_PORT)/
	@echo "Serving docs at http://localhost:$(DOCS_SERVE_PORT)/ — Ctrl+C to stop"
	@( sleep 1 && open "http://localhost:$(DOCS_SERVE_PORT)/" >/dev/null 2>&1 ) &
	@cd docs/public && python3 -m http.server $(DOCS_SERVE_PORT) --bind 127.0.0.1

CLAUDE_VERSION := $(shell curl -sf https://storage.googleapis.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases/latest 2>/dev/null || echo latest)

docker-build: ## Build the Docker container images (agent + chrome)
	docker build --build-arg CLAUDE_VERSION=$(CLAUDE_VERSION) --secret id=gitconfig,src=$(HOME)/.gitconfig -t loop-agent -f container/Dockerfile .
	docker build -t loop-chrome -f internal/container/image/chrome.Dockerfile internal/container/image/

run: build ## Build and run the bot
	./bin/loop serve

_sync-loop-overrides:
	@MAIN=$$(git worktree list --porcelain 2>/dev/null | awk '/^worktree /{print $$2; exit}'); \
	HERE=$$(pwd); \
	if [ -n "$$MAIN" ] && [ "$$MAIN" != "$$HERE" ] && [ -f "$$MAIN/.loop/setup.sh" ]; then \
		mkdir -p .loop; \
		if [ ! -f .loop/setup.sh ] || ! cmp -s "$$MAIN/.loop/setup.sh" .loop/setup.sh; then \
			cp "$$MAIN/.loop/setup.sh" .loop/setup.sh; \
			echo "Synced .loop/setup.sh from $$MAIN"; \
		fi; \
	fi

restart: install _sync-loop-overrides docker-build ## Install, stop and start the daemon
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

app-test: ## Run the frontend unit tests (vitest)
	cd app && npm install && npm test

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

# CodeQL's Go extractor lags the toolchain: it caps at the language version in
# go.mod and forces GOTOOLCHAIN=local, so it must run on the Go release that
# matches that cap, not the toolchain we build with. Keep these containers on
# CODEQL_GO_VERSION until the bundle supports a newer language version.
CODEQL_VERSION ?= v2.25.2
CODEQL_GO_VERSION ?= 1.26

codeql-download: ## Download CodeQL bundle into Docker volume (one-time)
	@echo "==> Downloading CodeQL $(CODEQL_VERSION) (linux64)..."
	@curl -fsSL "https://github.com/github/codeql-action/releases/download/codeql-bundle-$(CODEQL_VERSION)/codeql-bundle-linux64.tar.gz" \
		| docker run --rm -i --platform linux/amd64 -v loop-codeql:/opt/codeql golang:$(CODEQL_GO_VERSION) tar xz -C /opt/codeql
	@echo "==> Cached in volume loop-codeql"

codeql: ## Run CodeQL security analysis locally (via Docker)
	@docker rm -f loop-codeql 2>/dev/null || true
	docker run --rm --name loop-codeql --platform linux/amd64 \
		-v "$$(pwd)":/src:ro -v /src/app/node_modules \
		-v loop-codeql:/opt/codeql \
		-v loop-codeql-db:/db \
		-w /src \
		-e GOTOOLCHAIN=local \
		golang:$(CODEQL_GO_VERSION) bash -c '\
		set -e; \
		if [ ! -x /opt/codeql/codeql/codeql ]; then \
			echo "CodeQL not cached — run: make codeql-download" >&2; exit 1; \
		fi; \
		echo "==> Creating database..."; \
		/opt/codeql/codeql/codeql database create /db/loop --language=go --source-root=/src --overwrite \
			--command="go build -buildvcs=false -o /tmp/loop ./cmd/loop/"; \
		echo "==> Analyzing..."; \
		/opt/codeql/codeql/codeql database analyze /db/loop go-security-and-quality \
			--format=sarifv2.1.0 --output=/db/results.sarif; \
		echo "==> Results:"; \
		python3 -c "import json,sys; d=json.load(open(\"/db/results.sarif\")); rs=d.get(\"runs\",[{}])[0].get(\"results\",[]); [print(\"  \"+r[\"ruleId\"]+\" \"+r[\"locations\"][0][\"physicalLocation\"][\"artifactLocation\"][\"uri\"]+\":\"+str(r[\"locations\"][0][\"physicalLocation\"][\"region\"][\"startLine\"])+\" - \"+r[\"message\"][\"text\"]) for r in rs] or print(\"  No issues found.\"); sys.exit(len(rs))"; \
		'

deps-outdated: ## List outdated Go and npm dependencies (no changes made)
	@echo "== Go modules with newer versions =="
	@go list -u -m all 2>/dev/null | grep '\[' || echo "  all current"
	@echo "== npm packages (app/) with newer versions =="
	@cd app && npm outdated || true

clean: ## Remove build artifacts
	rm -rf bin/ app/resources/ coverage.out coverage.html
