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
        gosu ripgrep openssh-client unzip \
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
# --ignore-installed: the base image ships Debian's python3-packaging, which
# pip can't uninstall when pre-commit wants a newer one; pip's copy goes
# alongside it in /usr/local instead.
RUN pip install --break-system-packages --no-cache-dir --ignore-installed pre-commit uv
COPY --from=amazon/aws-cli:latest /usr/local/aws-cli /usr/local/aws-cli
RUN ln -s /usr/local/aws-cli/v2/current/bin/aws /usr/local/bin/aws \
    && ln -s /usr/local/aws-cli/v2/current/bin/aws_completer /usr/local/bin/aws_completer \
    && aws --version
ENV AWS_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt
ENV USE_BUILTIN_RIPGREP=0
# A release's latest tag comes from the release page's redirect, which has no
# rate limit, and its tarball from the download page. GitHub's pages return a
# 504 or an empty reply now and then, so every request retries on any error,
# and when a page still doesn't answer the tag or the tarball comes from
# GitHub's API instead.
ARG LOOP_VERSION=main
RUN ARCH=$(uname -m | sed 's/aarch64/arm64/' | sed 's/x86_64/amd64/') && \
    gh_curl() { curl -fsSL --retry 3 --retry-delay 2 --retry-all-errors "$@"; } && \
    latest_tag() { \
    gh_curl -I -o /dev/null -w '%{url_effective}' "https://github.com/$1/releases/latest" | sed -n 's#.*/releases/tag/##p' | grep . || \
    gh_curl "https://api.github.com/repos/$1/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | grep .; \
    } && \
    release_asset() { \
    gh_curl -o "$4" "https://github.com/$1/releases/download/$2/$3" || { \
    url=$(gh_curl "https://api.github.com/repos/$1/releases/tags/$2" | awk -v n="\"$3\"" '/"url": *".*\/releases\/assets\//{u=$0} /"name":/ && index($0, n){sub(/.*"url": *"/, "", u); sub(/".*/, "", u); print u; exit}') && \
    [ -n "$url" ] && gh_curl -H 'Accept: application/octet-stream' -o "$4" "$url"; \
    }; \
    } && \
    if [ "$LOOP_VERSION" != "main" ]; then \
    TAG="v${LOOP_VERSION#v}"; \
    else \
    TAG=$(latest_tag radutopala/loop); \
    fi && \
    release_asset radutopala/loop "$TAG" "loop_${TAG#v}_linux_${ARCH}.tar.gz" /tmp/loop.tar.gz && \
    tar -xzf /tmp/loop.tar.gz -C /usr/local/bin loop && rm /tmp/loop.tar.gz && \
    TAG=$(latest_tag radutopala/ticket) && \
    release_asset radutopala/ticket "$TAG" "tk_${TAG#v}_linux_${ARCH}.tar.gz" /tmp/tk.tar.gz && \
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
