#!/bin/bash
set -e

# Create container user matching host user (passed via env by runner).
# HOST_UID/HOST_GID pin the agent to the host's numeric IDs so that
# `docker exec` from the host (which sends numeric UID:GID via runc to
# avoid a /etc/passwd race against this useradd) lands on the same
# /etc/passwd entry the entrypoint creates. Without the pin, useradd
# auto-picks 1000 while a macOS host's UID is 501 — exec'd shells then
# run as a UID with no name and bash falls back to "I have no name!".
AGENT_USER="${HOST_USER:-agent}"
AGENT_HOME="${HOME:-/home/$AGENT_USER}"
mkdir -p "$AGENT_HOME"
USERADD_ARGS="-M -d $AGENT_HOME -s /bin/bash"
if [ -n "$HOST_GID" ]; then
    if ! getent group "$HOST_GID" >/dev/null 2>&1; then
        groupadd --gid "$HOST_GID" "$AGENT_USER" 2>/dev/null || true
    fi
    USERADD_ARGS="$USERADD_ARGS --gid $HOST_GID"
fi
if [ -n "$HOST_UID" ]; then
    USERADD_ARGS="$USERADD_ARGS --uid $HOST_UID --non-unique"
fi
useradd $USERADD_ARGS "$AGENT_USER" 2>/dev/null || true
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
# Runs as root (gosu below drops to the agent user). Listens on
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
        groupadd --system --gid "$SOCK_GID" dockerhost
        GROUP_NAME=dockerhost
    fi
    usermod -aG "$GROUP_NAME" "$AGENT_USER" 2>/dev/null || true
fi

mkdir -p "$AGENT_HOME/.local/bin"
ln -sf /usr/local/bin/claude "$AGENT_HOME/.local/bin/claude"
chown -R "$AGENT_USER":"$AGENT_USER" "$AGENT_HOME/.local" 2>/dev/null || true
export PATH="$AGENT_HOME/.local/bin:$AGENT_HOME/bin:$PATH"

# When LOOP_GATE_ENABLED=1, exec into the `loop syscallwrap` subcommand as
# root. The parent retains root (so the agent can't signal it —
# different-uid kill() is EPERM) and drops the child to $AGENT_USER via
# SysProcAttr.Credential after installing the seccomp filter. No gosu
# wrapper here.
if [ "$LOOP_GATE_ENABLED" = "1" ] && [ -x /usr/local/bin/loop ]; then
    exec /usr/local/bin/loop syscallwrap -- "$@"
fi
exec gosu "$AGENT_USER" "$@"
