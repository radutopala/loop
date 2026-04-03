#!/bin/bash

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${GREEN}=== Component Performance Test ===${NC}"

# Build loop binary
echo -e "${YELLOW}Building loop...${NC}"
CGO_ENABLED=0 go build -o bin/loop ./cmd/loop

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
  "api_addr": ":18222",
  "log_level": "warn",
  "memory": { "enabled": false },
  "browser": { "enabled": false }
}
EOF

echo -e "${YELLOW}Starting loop (HOME=$LOOP_HOME)...${NC}"
HOME="$LOOP_HOME" bin/loop serve &
LOOP_PID=$!
echo "loop started with PID: $LOOP_PID"

cleanup() {
    echo -e "\n${YELLOW}Shutting down loop...${NC}"
    kill "$LOOP_PID" 2>/dev/null || true
    wait "$LOOP_PID" 2>/dev/null || true
    rm -rf "$TMPDIR"
    echo -e "${GREEN}Cleanup done${NC}"
}
trap cleanup EXIT

# Wait for health endpoint
echo -e "${YELLOW}Waiting for loop to be ready...${NC}"
for i in $(seq 1 30); do
    if curl -sf http://localhost:18222/api/health > /dev/null 2>&1; then
        echo -e "${GREEN}loop is ready!${NC}"
        break
    fi
    echo "Waiting... attempt $i/30"
    sleep 1
done

if ! curl -sf http://localhost:18222/api/health > /dev/null 2>&1; then
    echo -e "${RED}ERROR: loop failed to start${NC}"
    exit 1
fi

# Pass through env vars
TEST_FLAGS="-run TestAPIPerfTestSuite"
if [ -n "$TEST_RUN" ]; then
    echo "Running specific test: $TEST_RUN"
    TEST_FLAGS="-run $TEST_RUN"
fi

# Run component tests
echo -e "${YELLOW}Running component tests...${NC}"
LOOP_BASE_URL="http://localhost:18222" \
LOOP_PID="$LOOP_PID" \
go test -timeout 120s -count=1 -v -tags=component ${TEST_FLAGS} ./test/component/...
