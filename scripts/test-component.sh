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

# Create temp directory for isolated config + DB
TMPDIR=$(mktemp -d)
LOOP_HOME="$TMPDIR"
LOOP_DIR="$LOOP_HOME/.loop"
mkdir -p "$LOOP_DIR"

# Write minimal config
cat > "$LOOP_DIR/config.json" <<EOF
{
  "platforms": ["local"],
  "db_path": "$LOOP_DIR/loop.db",
  "api_addr": ":8222",
  "log_level": "warn",
  "memory": { "enabled": false },
  "browser": { "enabled": false }
}
EOF

echo -e "${YELLOW}Starting loop (HOME=$LOOP_HOME)...${NC}"
HOME="$LOOP_HOME" bin/loop serve 2>/dev/null &
LOOP_PID=$!
echo "loop started with PID: $LOOP_PID"

cleanup() {
    echo -e "\n${YELLOW}Shutting down...${NC}"
    kill "$LOOP_PID" 2>/dev/null || true
    wait "$LOOP_PID" 2>/dev/null || true
    if [ -n "$VITE_PID" ]; then
        kill "$VITE_PID" 2>/dev/null || true
        wait "$VITE_PID" 2>/dev/null || true
    fi
    rm -rf "$TMPDIR"
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

# Run component tests
echo -e "${YELLOW}Running component tests...${NC}"
LOOP_BASE_URL="http://localhost:8222" \
LOOP_APP_URL="${LOOP_APP_URL:-http://localhost:5173}" \
LOOP_PID="$LOOP_PID" \
CHROME_CDP_URL="${CHROME_CDP_URL:-}" \
GODOG_CONCURRENCY="${GODOG_CONCURRENCY:-1}" \
go test -timeout 900s -count=1 -v -tags=component ${TEST_FLAGS} ./test/component/...
