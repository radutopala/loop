#!/bin/sh
set -e

# Create container user matching host user (passed via env by runner)
AGENT_USER="${HOST_USER:-agent}"
AGENT_HOME="${HOME:-/home/$AGENT_USER}"
mkdir -p "$AGENT_HOME"
adduser -D -h "$AGENT_HOME" -H "$AGENT_USER" 2>/dev/null || true
mkdir -p "$AGENT_HOME/.claude"
chown "$AGENT_USER":"$AGENT_USER" "$AGENT_HOME" 2>/dev/null || true
chown "$AGENT_USER":"$AGENT_USER" "$AGENT_HOME/.claude" 2>/dev/null || true

# Grant user access to the Docker socket if mounted
if [ -S /var/run/docker.sock ]; then
    SOCK_GID=$(stat -c '%g' /var/run/docker.sock)
    GROUP_NAME=$(awk -F: -v gid="$SOCK_GID" '$3 == gid {print $1; exit}' /etc/group)
    if [ -z "$GROUP_NAME" ]; then
        addgroup -S -g "$SOCK_GID" dockerhost
        GROUP_NAME=dockerhost
    fi
    addgroup "$AGENT_USER" "$GROUP_NAME" 2>/dev/null || true
fi

exec su-exec "$AGENT_USER" "$@"
