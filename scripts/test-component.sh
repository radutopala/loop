#!/bin/bash

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

# Stitch the per-section docs clips into one journey.mp4. Each clip is named
# NN_<section>.mp4 (NN = feature order), so a plain lexical sort gives the
# journey order — a missing/failed section just leaves a numbering gap and is
# skipped. Called only for a full docs-capture run (GODOG_TAGS=@docs);
# per-section runs leave journey.mp4 alone.
stitch_journey() {
    local vdir="docs/videos"
    if ! command -v ffmpeg > /dev/null 2>&1; then
        echo -e "${YELLOW}stitch: ffmpeg not found; skipping journey.mp4${NC}"
        return 0
    fi
    local list="$vdir/.journey-concat.txt"
    : > "$list"
    local n=0
    for f in $(ls "$vdir"/[0-9][0-9]_*.mp4 2>/dev/null | sort); do
        echo "file '$(basename "$f")'" >> "$list"
        n=$((n + 1))
    done
    if [ "$n" -eq 0 ]; then
        echo -e "${YELLOW}stitch: no section clips found; skipping journey.mp4${NC}"
        rm -f "$list"
        return 0
    fi
    echo -e "${YELLOW}Stitching $n section clips into $vdir/journey.mp4...${NC}"
    # All section clips are encoded identically (libx264/yuv420p/30fps), so try a
    # fast stream-copy concat first; fall back to a re-encode if copy rejects it.
    if ! ffmpeg -y -f concat -safe 0 -i "$list" -c copy -movflags +faststart "$vdir/journey.mp4" > /dev/null 2>&1; then
        ffmpeg -y -f concat -safe 0 -i "$list" -vf "fps=30" -c:v libx264 -pix_fmt yuv420p -movflags +faststart "$vdir/journey.mp4" \
            || { echo -e "${RED}stitch: ffmpeg concat failed${NC}"; rm -f "$list"; return 1; }
    fi
    rm -f "$list"
    echo -e "${GREEN}Wrote $vdir/journey.mp4 ($n clips)${NC}"
}

echo -e "${GREEN}=== Component Tests ===${NC}"

# Full docs-capture run: clear stale section clips so a removed/failed section
# can't leave a stale clip in the stitched journey. (Per-section runs keep them.)
if [ -n "$LOOP_DOCS_CAPTURE" ] && [ "$GODOG_TAGS" = "@docs" ]; then
    rm -f docs/videos/*.mp4 2>/dev/null || true
fi

# Install dependencies if missing (CI runners, non-prebuilt containers).
if [ -f /.dockerenv ] && ! command -v chromium &> /dev/null && [ -z "$CHROME_CDP_URL" ]; then
    echo -e "${YELLOW}Installing container dependencies...${NC}"
    apt-get update -qq > /dev/null 2>&1
    apt-get install -yqq curl chromium > /dev/null 2>&1
    if ! command -v node &> /dev/null; then
        curl -fsSL https://deb.nodesource.com/setup_24.x | bash - > /dev/null 2>&1
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
# Memory + embeddings are off for normal/CI runs (Ollama is heavy). Docs
# capture turns them on so the Memory panel is populated from indexed *.md.
MEMORY_BLOCK='"memory": { "enabled": false },'
# Agent containers reach the loop API at host.docker.internal by default. In
# docs capture the daemon runs INSIDE a container, so host.docker.internal:8222
# would hit a DIFFERENT daemon on the Docker host (e.g. a dev instance) instead
# of this one — breaking the browser panel (the agent would drive the wrong
# Chrome). Point agents at this daemon's own container IP over the Docker bridge.
AGENT_API_BLOCK=""
DOCS_AUTH=""
if [ -n "$LOOP_DOCS_CAPTURE" ]; then
    # ollama_url is the host-mode fallback; when loop runs in a container (as it
    # does here) the embedder auto-connects to the ollama sidecar's bridge IP
    # (container-to-container), so this localhost value isn't actually used.
    # (Requires the test-runner image to have the docker CLI — docker-cli in
    # scripts/test-runner.Dockerfile.)
    MEMORY_BLOCK='"memory": { "enabled": true, "reindex_interval_sec": 30, "embeddings": { "provider": "ollama", "model": "nomic-embed-text", "ollama_url": "http://localhost:11434" } },'
    CONTAINER_IP=$(hostname -i 2>/dev/null | awk '{print $1}')
    if [ -n "$CONTAINER_IP" ]; then
        AGENT_API_BLOCK="\"api_advertise_url\": \"http://$CONTAINER_IP:8222\","
        echo -e "${YELLOW}Docs capture: agents will reach this daemon at http://$CONTAINER_IP:8222${NC}"
    fi
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
            # Empty config — the container runner reads this via copy_files and
            # merges in the consent flags itself (onboarding, bypass-permissions,
            # per-workdir trust), so the live agent boots straight to the prompt.
            echo '{}' > "$LOOP_HOME/.claude.json"
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
            # Agent containers normally pull a RELEASED loop binary (see the agent
            # image Dockerfile). For docs capture we want the agent's mcp-browser
            # to run THIS build — so its browser actions route through the daemon
            # API and share the panel's Chrome tab (otherwise the panel preview
            # shows about:blank while the agent drives its own tab). Stage the
            # freshly-built binary under the host-shared LOOP_HOME and bind-mount
            # it over /usr/local/bin/loop in each agent container.
            cp bin/loop "$LOOP_HOME/loop" && chmod 0755 "$LOOP_HOME/loop"
            DOCS_AUTH="\"claude_code_oauth_token\": \"$DOCS_TOKEN\",
  \"envs\": { \"NODE_TLS_REJECT_UNAUTHORIZED\": \"0\", \"NODE_NO_WARNINGS\": \"1\", \"HOST_USER\": \"agent\", \"HOST_UID\": \"1000\", \"HOST_GID\": \"1000\" },
  \"gates\": { \"agentgate\": { \"enabled\": true, \"command_rules\": [ { \"commands\": [\"git\"], \"args_patterns\": [\"commit\", \"push\"], \"decision\": \"approve\", \"message\": \"git commit/push (approval required)\" } ] } },
  \"mounts\": [\"~/.claude:~/.claude\", \"$LOOP_HOME/loop:/usr/local/bin/loop\"],
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
  $AGENT_API_BLOCK
  "log_level": "warn",
  $MEMORY_BLOCK
  "browser": { "enabled": true },
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

# Run component tests. A full docs-capture run drives ~22 per-section scenarios
# (each its own browser + live agent + MP4 encode), so allow generous headroom.
# Override with GO_TEST_TIMEOUT.
TEST_TIMEOUT=900s
[ -n "$LOOP_DOCS_CAPTURE" ] && TEST_TIMEOUT=2700s
[ -n "$GO_TEST_TIMEOUT" ] && TEST_TIMEOUT="$GO_TEST_TIMEOUT"
echo -e "${YELLOW}Running component tests (timeout $TEST_TIMEOUT)...${NC}"
TEST_RC=0
LOOP_BASE_URL="http://localhost:8222" \
LOOP_APP_URL="${LOOP_APP_URL:-http://localhost:5173}" \
LOOP_PID="$LOOP_PID" \
CHROME_CDP_URL="${CHROME_CDP_URL:-}" \
GODOG_CONCURRENCY="${GODOG_CONCURRENCY:-1}" \
go test -timeout "$TEST_TIMEOUT" -count=1 -v -tags=component ${TEST_FLAGS} ./test/component/... || TEST_RC=$?

# Full docs-capture run: stitch the per-section clips into journey.mp4. Done even
# on a partial failure so the montage reflects whatever sections were captured.
if [ -n "$LOOP_DOCS_CAPTURE" ] && [ "$GODOG_TAGS" = "@docs" ]; then
    stitch_journey || true
fi
exit "$TEST_RC"
