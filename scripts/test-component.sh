#!/bin/bash

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${GREEN}=== Component Tests ===${NC}"

# Install dependencies if missing (CI runners, non-prebuilt containers).
if [ -f /.dockerenv ] && ! command -v chromium &> /dev/null && [ -z "$CHROME_CDP_URL" ]; then
    echo -e "${YELLOW}Installing container dependencies...${NC}"
    apt-get update -qq > /dev/null 2>&1
    apt-get install -yqq curl chromium > /dev/null 2>&1
    if ! command -v node &> /dev/null; then
        curl -fsSL https://deb.nodesource.com/setup_22.x | bash - > /dev/null 2>&1
        apt-get install -yqq nodejs > /dev/null 2>&1
    fi
fi

# Build loop binary
echo -e "${YELLOW}Building loop...${NC}"
CGO_ENABLED=0 go build -buildvcs=false -o bin/loop ./cmd/loop

# Start Vite dev server for frontend tests if app/ exists
VITE_PID=""
if [ -d "app" ] && [ -z "$LOOP_APP_URL" ]; then
    echo -e "${YELLOW}Starting Vite dev server...${NC}"
    (cd app && npm install --silent > /dev/null 2>&1 && LOOP_API_URL="http://localhost:8222" npx vite --host 0.0.0.0 --port 5173 --config vite.browser.config.ts > /dev/null 2>&1) &
    VITE_PID=$!
    export LOOP_APP_URL="http://localhost:5173"
    for i in $(seq 1 30); do
        if curl -sf http://localhost:5173 > /dev/null 2>&1; then
            echo -e "${GREEN}Vite dev server is ready!${NC}"
            break
        fi
        echo "Waiting for Vite... attempt $i/30"
        sleep 2
    done
    if ! curl -sf http://localhost:5173 > /dev/null 2>&1; then
        echo -e "${RED}ERROR: Vite dev server failed to start${NC}"
        exit 1
    fi
fi

# Create temp directory for isolated config + DB.
# When running inside a container with the host Docker socket mounted, use a
# fixed path so sibling containers spawned via the socket can bind-mount the
# same files from the host.
if [ -f /.dockerenv ] && [ -S /var/run/docker.sock ]; then
    TMPDIR=/tmp/loop-bdd-data
    rm -rf "$TMPDIR"/* 2>/dev/null || true
    mkdir -p "$TMPDIR"
else
    TMPDIR=$(mktemp -d)
fi
LOOP_HOME="$TMPDIR"
LOOP_DIR="$LOOP_HOME/.loop"
mkdir -p "$LOOP_DIR"

# Docs-capture: make agent containers run live Claude. Reuse the real OAuth
# token + TLS-relaxing envs (corporate MITM), seed the onboarding marker, and
# force a non-root agent uid — this sandbox runs loop as root, and claude
# refuses --dangerously-skip-permissions as root. Gated on LOOP_DOCS_CAPTURE so
# normal/CI runs stay hermetic and unauthenticated.
DOCS_AUTH=""
if [ -n "$LOOP_DOCS_CAPTURE" ]; then
    # Channel workdirs (created by BDD steps) live under this shared, writable
    # base so sibling agent containers can bind-mount them by the same path.
    export LOOP_DOCS_WORKDIR_BASE="$TMPDIR"
    # This sandbox runs loop as root, but agent containers run Claude as the
    # non-root agent user (uid 1000, see envs below). docker-exec terminals
    # derive their exec user from the loop PROCESS env, so export the same uid
    # here — otherwise the Docker Agent terminal execs as root and Claude
    # refuses --dangerously-skip-permissions.
    export HOST_UID=1000 HOST_GID=1000
    if [ -n "$LOOP_DOCS_HOST_CONFIG" ] && [ -f "$LOOP_DOCS_HOST_CONFIG" ]; then
        # The config is HJSON (comments, unquoted keys, trailing commas), so a
        # strict JSON parser (jq/python) can't be relied on — extract the token
        # tolerantly. Matches both "key": "v" and unquoted key: "v" forms.
        DOCS_TOKEN=$(sed -nE 's/.*claude_code_oauth_token[^:]*:[[:space:]]*"([^"]+)".*/\1/p' "$LOOP_DOCS_HOST_CONFIG" 2>/dev/null | head -1 || true)
        if [ -n "$DOCS_TOKEN" ]; then
            # bypassPermissionsModeAccepted pre-accepts the interactive
            # "Bypass Permissions mode" consent the TUI shows on first launch
            # under --dangerously-skip-permissions; without it the Docker Agent
            # terminal stalls on that prompt instead of resuming the session.
            echo '{"hasCompletedOnboarding":true,"bypassPermissionsModeAccepted":true}' > "$LOOP_HOME/.claude.json"
            # Fresh, isolated Claude session store for this run, bind-mounted from
            # the loop server's own HOME so BOTH the agent containers AND the loop
            # server see the same ~/.claude/projects/<workdir>/*.jsonl files: the
            # agent writes session files there (so a 2nd turn can `claude --resume`
            # the 1st), and the Sessions panel — which reads the server's
            # $HOME/.claude/projects — can list them. A named volume would only be
            # visible to the agent containers, leaving the Sessions panel empty.
            # Pre-create + chown to the agent uid (1000) so the non-root agent can
            # write into the bind source (binds aren't auto-chowned like volumes).
            mkdir -p "$LOOP_HOME/.claude"
            chown 1000:1000 "$LOOP_HOME/.claude" 2>/dev/null || true
            DOCS_AUTH="\"claude_code_oauth_token\": \"$DOCS_TOKEN\",
  \"envs\": { \"NODE_TLS_REJECT_UNAUTHORIZED\": \"0\", \"NODE_NO_WARNINGS\": \"1\", \"HOST_UID\": \"1000\", \"HOST_GID\": \"1000\" },
  \"gates\": { \"agentgate\": { \"enabled\": false } },
  \"mounts\": [\"~/.claude:~/.claude\"],
  \"copy_files\": [\"~/.claude.json\"],"
            echo -e "${YELLOW}Docs capture: injecting Claude auth + non-root agent uid for live runs${NC}"
        else
            echo -e "${RED}WARNING: LOOP_DOCS_CAPTURE set but no claude_code_oauth_token in $LOOP_DOCS_HOST_CONFIG${NC}"
        fi
    fi
fi

# Write minimal config
cat > "$LOOP_DIR/config.json" <<EOF
{
  "platforms": ["local"],
  $DOCS_AUTH
  "db_path": "$LOOP_DIR/loop.db",
  "api_addr": ":8222",
  "log_level": "warn",
  "memory": { "enabled": false },
  "browser": { "enabled": false },
  "workflow_bash_local": true,
  "workflows": [
    {
      "name": "bdd-test-workflow",
      "description": "Simple test workflow for BDD scenarios",
      "inputs": {
        "message": { "description": "A test message", "required": false, "default": "hello" }
      },
      "nodes": [
        { "id": "greet", "type": "bash", "script": "echo {{.Inputs.message}}" }
      ]
    }
  ]
}
EOF

echo -e "${YELLOW}Starting loop (HOME=$LOOP_HOME)...${NC}"
LOOP_STDERR="$LOOP_DIR/serve-stderr.log"
HOME="$LOOP_HOME" bin/loop serve 2>"$LOOP_STDERR" &
LOOP_PID=$!
echo "loop started with PID: $LOOP_PID"

cleanup() {
    echo -e "\n${YELLOW}Shutting down...${NC}"
    # Always dump server log tail for debugging CI failures.
    if [ -s "$LOOP_STDERR" ]; then
        echo -e "${YELLOW}=== Loop server stderr (last 100 lines) ===${NC}"
        tail -100 "$LOOP_STDERR"
        echo -e "${YELLOW}=== End server stderr ===${NC}"
    fi
    kill "$LOOP_PID" 2>/dev/null || true
    wait "$LOOP_PID" 2>/dev/null || true
    if [ -n "$VITE_PID" ]; then
        kill "$VITE_PID" 2>/dev/null || true
        wait "$VITE_PID" 2>/dev/null || true
    fi
    # Remove containers spawned by workflow bash nodes during the test run.
    if command -v docker &> /dev/null; then
        docker ps -aq --filter "name=loop-bdd-" | xargs -r docker rm -f 2>/dev/null || true
    fi
    rm -rf "$TMPDIR" 2>/dev/null || rm -rf "$TMPDIR"/* 2>/dev/null || true
    echo -e "${GREEN}Cleanup done${NC}"
}
trap cleanup EXIT

# Wait for health endpoint
echo -e "${YELLOW}Waiting for loop to be ready...${NC}"
for i in $(seq 1 30); do
    if curl -sf http://localhost:8222/api/health > /dev/null 2>&1; then
        echo -e "${GREEN}loop is ready!${NC}"
        break
    fi
    echo "Waiting... attempt $i/30"
    sleep 1
done

if ! curl -sf http://localhost:8222/api/health > /dev/null 2>&1; then
    echo -e "${RED}ERROR: loop failed to start${NC}"
    exit 1
fi

# Pass through env vars — run all component tests by default.
TEST_FLAGS=""
if [ -n "$TEST_RUN" ]; then
    echo "Running specific test: $TEST_RUN"
    TEST_FLAGS="-run $TEST_RUN"
fi

# Run component tests. docs-capture runs one long end-to-end journey (live agent
# reply + a full panel tour + MP4 encode); the per-scenario budget is 120s, so
# allow headroom here. Override with GO_TEST_TIMEOUT.
TEST_TIMEOUT=900s
[ -n "$LOOP_DOCS_CAPTURE" ] && TEST_TIMEOUT=1200s
[ -n "$GO_TEST_TIMEOUT" ] && TEST_TIMEOUT="$GO_TEST_TIMEOUT"
echo -e "${YELLOW}Running component tests (timeout $TEST_TIMEOUT)...${NC}"
LOOP_BASE_URL="http://localhost:8222" \
LOOP_APP_URL="${LOOP_APP_URL:-http://localhost:5173}" \
LOOP_PID="$LOOP_PID" \
CHROME_CDP_URL="${CHROME_CDP_URL:-}" \
GODOG_CONCURRENCY="${GODOG_CONCURRENCY:-1}" \
go test -timeout "$TEST_TIMEOUT" -count=1 -v -tags=component ${TEST_FLAGS} ./test/component/...
