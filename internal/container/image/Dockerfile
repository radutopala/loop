FROM golang:1.27 AS builder
ENV GOTOOLCHAIN=auto
RUN go install golang.org/x/tools/gopls@latest

FROM golang:1.27
ENV GOTOOLCHAIN=auto
# Node comes from nodesource (24.x LTS) instead of Debian's
# distro package — Debian ships Node 20, which is end-of-life. Keep the
# version in sync with scripts/test-runner.Dockerfile and the CI
# node-version pins.
#
# npm is pinned on its own (Node 24 bundles npm 11). npm 12 blocks dependency
# install scripts unless the project's package.json allowScripts policy
# allows them, and refuses git and tarball-URL dependencies by default.
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
    && npm install -g npm@12.1.0 && npm cache clean --force \
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
ARG LOOP_VERSION=main
RUN ARCH=$(uname -m | sed 's/aarch64/arm64/' | sed 's/x86_64/amd64/') && \
    if [ "$LOOP_VERSION" != "main" ]; then \
    TAG="v${LOOP_VERSION#v}"; \
    curl -fsSL -o /tmp/loop.tar.gz "https://github.com/radutopala/loop/releases/download/${TAG}/loop_${LOOP_VERSION#v}_linux_${ARCH}.tar.gz" && \
    tar -xzf /tmp/loop.tar.gz -C /usr/local/bin loop && rm /tmp/loop.tar.gz; \
    else \
    TAG=$(curl -fsSL https://api.github.com/repos/radutopala/loop/releases/latest | grep -o '"tag_name": *"[^"]*"' | head -1 | cut -d'"' -f4) && \
    V="${TAG#v}" && \
    curl -fsSL -o /tmp/loop.tar.gz "https://github.com/radutopala/loop/releases/download/${TAG}/loop_${V}_linux_${ARCH}.tar.gz" && \
    tar -xzf /tmp/loop.tar.gz -C /usr/local/bin loop && rm /tmp/loop.tar.gz; \
    fi
RUN ARCH=$(uname -m | sed 's/aarch64/arm64/' | sed 's/x86_64/amd64/') && \
    TAG=$(curl -fsSL https://api.github.com/repos/radutopala/ticket/releases/latest | grep -o '"tag_name": *"[^"]*"' | head -1 | cut -d'"' -f4) && \
    V="${TAG#v}" && \
    curl -fsSL -o /tmp/tk.tar.gz "https://github.com/radutopala/ticket/releases/download/${TAG}/tk_${V}_linux_${ARCH}.tar.gz" && \
    tar -xzf /tmp/tk.tar.gz -C /usr/local/bin tk && rm /tmp/tk.tar.gz
ARG CLAUDE_VERSION=latest
LABEL loop.version="${LOOP_VERSION}" \
    loop.claude_version="${CLAUDE_VERSION}"
RUN curl -fsSL https://claude.ai/install.sh | bash && cp -L ~/.local/bin/claude /usr/local/bin/claude && rm -rf ~/.local/share/claude ~/.local/bin/claude
COPY --from=builder /go/bin/gopls /usr/local/bin/gopls
COPY setup.sh /usr/local/bin/setup.sh
RUN --mount=type=secret,id=gitconfig,target=/root/.gitconfig chmod +x /usr/local/bin/setup.sh && /usr/local/bin/setup.sh
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
COPY agent-bashrc /etc/loop/bashrc
RUN chmod +x /usr/local/bin/entrypoint.sh
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["claude"]
