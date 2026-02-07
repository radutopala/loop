#!/bin/sh
set -e

# Configure Claude Code MCP server if CHANNEL_ID and API_URL are set
if [ -n "$CHANNEL_ID" ] && [ -n "$API_URL" ]; then
    cat > /work/.mcp.json <<EOF
{
  "mcpServers": {
    "loop-scheduler": {
      "command": "/usr/local/bin/loop",
      "args": ["mcp", "--channel-id", "$CHANNEL_ID", "--api-url", "$API_URL"]
    }
  }
}
EOF
fi

cd /work

CLAUDE_ARGS="--print --output-format json --dangerously-skip-permissions"
if [ -n "$SESSION_ID" ]; then
    CLAUDE_ARGS="$CLAUDE_ARGS --resume $SESSION_ID"
fi

exec claude $CLAUDE_ARGS "$@"
