#!/bin/sh
set -e

# Create container user matching host user (passed via env by runner)
AGENT_USER="${HOST_USER:-agent}"
AGENT_HOME="${HOME:-/home/$AGENT_USER}"
mkdir -p "$AGENT_HOME"
adduser -D -h "$AGENT_HOME" -H "$AGENT_USER" 2>/dev/null || true
chown "$AGENT_USER":"$AGENT_USER" "$AGENT_HOME" 2>/dev/null || true

# Fix ownership of paths that need to be writable by the agent user
# (named volumes created as root, files copied via CopyToContainer, etc.).
# CHOWN_PATHS is set by the runner with colon-separated container paths.
if [ -n "$CHOWN_PATHS" ]; then
    IFS=:
    for path in $CHOWN_PATHS; do
        if [ -d "$path" ]; then
            chown -R "$AGENT_USER":"$AGENT_USER" "$path" 2>/dev/null || true
        elif [ -f "$path" ]; then
            chown "$AGENT_USER":"$AGENT_USER" "$path" 2>/dev/null || true
        fi
    done
    unset IFS
fi

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

mkdir -p "$AGENT_HOME/.local/bin"
ln -sf /usr/local/bin/claude "$AGENT_HOME/.local/bin/claude"
chown -R "$AGENT_USER":"$AGENT_USER" "$AGENT_HOME/.local" 2>/dev/null || true
export PATH="$AGENT_HOME/.local/bin:$AGENT_HOME/bin:$PATH"
exec su-exec "$AGENT_USER" "$@"
