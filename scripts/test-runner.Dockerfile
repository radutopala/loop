# Pinned to the exact patch release in go.mod: the official golang images set
# GOTOOLCHAIN=local, so a runner older than the go directive cannot build the
# module. The floating 1.27 tag also left CI stale, because ensure-test-runner
# keys its cache on this file's hash and never noticed upstream moving.
FROM golang:1.27.1

# The Docker CLI and buildx come from Docker's own apt repository rather than
# Debian's, which pins buildx at 0.13.1 and the CLI at 26.1.5 — matching the
# agent images. Without the buildx plugin every `docker build` in a test falls
# back to the deprecated classic builder, which cannot cross-build.
#
# Node 24 and npm 12.1.0 match the agent images (see internal/container/image/Dockerfile).
RUN apt-get update -qq && \
    apt-get install -yqq --no-install-recommends \
        curl chromium ffmpeg fluidsynth fluid-soundfont-gm && \
    install -m 0755 -d /etc/apt/keyrings && \
    curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc && \
    chmod a+r /etc/apt/keyrings/docker.asc && \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian $(. /etc/os-release && echo $VERSION_CODENAME) stable" \
        > /etc/apt/sources.list.d/docker.list && \
    curl -fsSL https://deb.nodesource.com/setup_24.x | bash - && \
    apt-get update -qq && \
    apt-get install -yqq --no-install-recommends \
        docker-ce-cli docker-buildx-plugin nodejs && \
    npm install -g npm@12.1.0 && npm cache clean --force && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

WORKDIR /app
