#!/bin/sh
set -e

cd /work

CLAUDE_ARGS="--print --output-format json --dangerously-skip-permissions"
if [ -n "$SESSION_ID" ]; then
    CLAUDE_ARGS="$CLAUDE_ARGS --resume $SESSION_ID"
fi

exec claude $CLAUDE_ARGS "$@"
