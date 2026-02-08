#!/bin/sh
set -e

# Grant agent access to the Docker socket if mounted
if [ -S /var/run/docker.sock ]; then
    SOCK_GID=$(stat -c '%g' /var/run/docker.sock)
    GROUP_NAME=$(awk -F: -v gid="$SOCK_GID" '$3 == gid {print $1; exit}' /etc/group)
    if [ -z "$GROUP_NAME" ]; then
        addgroup -S -g "$SOCK_GID" dockerhost
        GROUP_NAME=dockerhost
    fi
    addgroup agent "$GROUP_NAME" 2>/dev/null || true
fi

CLAUDE_ARGS="--print --output-format json --dangerously-skip-permissions"
if [ -n "$SESSION_ID" ]; then
    CLAUDE_ARGS="$CLAUDE_ARGS --resume $SESSION_ID"
fi

exec su-exec agent claude $CLAUDE_ARGS "$@"
