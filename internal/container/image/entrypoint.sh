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

# Start the in-container docker HTTP reverse-proxy if enabled by the host.
# Runs as root (su-exec below drops to the agent user). Listens on
# /var/run/docker.sock (tmpfs inside the container) and reverse-proxies to
# /var/run/docker.sock.host (the real daemon socket, bind-mounted :ro by
# the host). The agent only ever sees the proxy socket — direct access to
# .sock.host is denied by the seccomp gate's path rules.
if [ "$LOOP_DOCKERPROXY_ENABLED" = "1" ] && [ -x /usr/local/bin/loop ]; then
    if [ ! -S /var/run/docker.sock.host ]; then
        echo "entrypoint: LOOP_DOCKERPROXY_ENABLED=1 but /var/run/docker.sock.host missing" >&2
        exit 1
    fi
    /usr/local/bin/loop dockerproxy &
    # Wait for the proxy socket to appear — bounded so a hung proxy fails
    # the container start cleanly rather than racing with the agent.
    i=0
    while [ $i -lt 20 ]; do
        [ -S /var/run/docker.sock ] && break
        sleep 0.1
        i=$((i + 1))
    done
    if [ ! -S /var/run/docker.sock ]; then
        echo "entrypoint: loop-dockerproxy failed to create /var/run/docker.sock" >&2
        exit 1
    fi
fi

# Grant user access to the Docker socket if mounted. Whether it's the real
# host socket (legacy / Linux direct-mount) or the in-container proxy socket
# created above, this block adds the agent to the owning GID so the agent
# can dial it.
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

# When LOOP_GATE_ENABLED=1, exec into the `loop syscallwrap` subcommand as
# root. The parent retains root (so the agent can't signal it —
# different-uid kill() is EPERM) and drops the child to $AGENT_USER via
# SysProcAttr.Credential after installing the seccomp filter. No su-exec
# wrapper here.
if [ "$LOOP_GATE_ENABLED" = "1" ] && [ -x /usr/local/bin/loop ]; then
    exec /usr/local/bin/loop syscallwrap -- "$@"
fi
exec su-exec "$AGENT_USER" "$@"
