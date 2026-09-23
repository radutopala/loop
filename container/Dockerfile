# `make docker-build` overrides this empty stage with the host's module
# download cache (--build-context gomodcache=...), which the builder reads as
# a local GOPROXY so modules the host already fetched aren't downloaded again.
# Anything missing there, or a plain `docker build`, falls through to the
# network proxy.
FROM scratch AS gomodcache

FROM golang:1.27 AS builder
ENV GOTOOLCHAIN=auto
WORKDIR /src
COPY go.mod go.sum ./
# The module and build caches are BuildKit cache mounts, so they persist
# across builds without landing in a layer: a rebuild only fetches what
# changed instead of re-downloading every dependency of the @latest installs.
RUN --mount=type=bind,from=gomodcache,target=/hostmod \
    --mount=type=cache,target=/go/pkg/mod \
    GOPROXY=file:///hostmod,https://proxy.golang.org,direct go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go generate ./internal/readme/ && \
    go build -o /loop ./cmd/loop
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go install github.com/radutopala/ticket/cmd/tk@latest && \
    go install golang.org/x/tools/gopls@latest

FROM golang:1.27
ENV GOTOOLCHAIN=auto
# Node comes from nodesource (24.x LTS, npm bundled) instead of Debian's
# distro package — Debian ships Node 20, which is end-of-life. Keep the
# version in sync with internal/container/image/Dockerfile,
# scripts/test-runner.Dockerfile, and the CI node-version pins.
#
# The Docker CLI and its plugins come from Docker's own apt repository rather
# than Debian's, which pins buildx at 0.13.1 and the CLI at 26.1.5. Without the
# buildx plugin a `docker build --platform` falls back to the classic builder,
# which cannot cross-build and dies at the first COPY with "does not provide
# the specified platform" on an arm64 host. docker-compose-plugin drops the
# hyphenated /usr/bin/docker-compose that Debian's package installed, so a shim
# keeps `docker-compose ...` working for projects whose scripts still use it.
RUN apt-get update && apt-get install -y --no-install-recommends \
        bash ca-certificates curl git git-lfs make patch \
        gosu ripgrep openssh-client \
        python3 python3-pip python3-venv \
        gcc libc6-dev \
    && install -m 0755 -d /etc/apt/keyrings \
    && curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc \
    && chmod a+r /etc/apt/keyrings/docker.asc \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian $(. /etc/os-release && echo $VERSION_CODENAME) stable" \
        > /etc/apt/sources.list.d/docker.list \
    && curl -fsSL https://deb.nodesource.com/setup_24.x | bash - \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
        docker-ce-cli docker-buildx-plugin docker-compose-plugin nodejs \
    && printf '#!/bin/sh\nexec docker compose "$@"\n' > /usr/local/bin/docker-compose \
    && chmod 0755 /usr/local/bin/docker-compose \
    && rm -rf /var/lib/apt/lists/*
# Docker Desktop's VirtioFS intermittently stats host bind mounts as uid 0
# instead of the host uid, which makes git abort with "detected dubious
# ownership" on a random fraction of calls — the flap hits the worktree dir
# more often than .git, so it looks like a ghost. safe.directory is additive
# across config scopes, so a system-level entry survives the host ~/.gitconfig
# mounted over the agent's global config. The trust check buys nothing here
# anyway: the container is single-tenant, runs with permissions skipped, and
# only ever mounts the user's own directories.
RUN git config --system --add safe.directory '*'
# The git-lfs postinst registers the system-level lfs filters in
# /etc/gitconfig, so a repo whose .gitattributes routes paths through lfs
# works without further setup — and system scope survives the host
# ~/.gitconfig being mounted over the container's global config.
RUN pip install --break-system-packages --no-cache-dir pre-commit uv awscli
ENV USE_BUILTIN_RIPGREP=0
# setup.sh runs before the per-build ARGs and the freshly built binaries, so
# its layer stays cached until the script itself changes. The Go caches are
# cache mounts shared with the builder stage, so the script's go installs
# don't hit the network for modules an earlier build already fetched.
COPY internal/container/image/setup.sh .loop/setup.s[h] /usr/local/bin/
RUN --mount=type=secret,id=gitconfig,target=/root/.gitconfig \
    --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    chmod +x /usr/local/bin/setup.sh && /usr/local/bin/setup.sh
ARG LOOP_VERSION=dev
ARG CLAUDE_VERSION=latest
LABEL loop.version="${LOOP_VERSION}" \
      loop.claude_version="${CLAUDE_VERSION}"
RUN curl -fsSL https://claude.ai/install.sh | bash && cp -L ~/.local/bin/claude /usr/local/bin/claude && rm -rf ~/.local/share/claude ~/.local/bin/claude
COPY --from=builder /loop /usr/local/bin/loop
COPY --from=builder /go/bin/tk /usr/local/bin/tk
COPY --from=builder /go/bin/gopls /usr/local/bin/gopls
COPY internal/container/image/entrypoint.sh /usr/local/bin/entrypoint.sh
COPY internal/container/image/agent-bashrc /etc/loop/bashrc
RUN chmod +x /usr/local/bin/entrypoint.sh
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["claude"]
