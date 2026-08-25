FROM golang:1.27 AS builder
ENV GOTOOLCHAIN=auto
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go generate ./internal/readme/ && \
    go build -o /loop ./cmd/loop
RUN go install github.com/radutopala/ticket/cmd/tk@latest
RUN go install golang.org/x/tools/gopls@latest

FROM golang:1.27
ENV GOTOOLCHAIN=auto
# Node comes from nodesource (24.x LTS, npm bundled) instead of Debian's
# distro package — Debian ships Node 20, which is end-of-life. Keep the
# version in sync with internal/container/image/Dockerfile,
# scripts/test-runner.Dockerfile, and the CI node-version pins.
RUN apt-get update && apt-get install -y --no-install-recommends \
        bash ca-certificates curl git make patch \
        docker-cli docker-compose \
        gosu ripgrep openssh-client \
        python3 python3-pip python3-venv \
        gcc libc6-dev \
    && curl -fsSL https://deb.nodesource.com/setup_24.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*
RUN pip install --break-system-packages --no-cache-dir pre-commit uv awscli
ENV USE_BUILTIN_RIPGREP=0
ARG LOOP_VERSION=dev
ARG CLAUDE_VERSION=latest
LABEL loop.version="${LOOP_VERSION}" \
      loop.claude_version="${CLAUDE_VERSION}"
RUN curl -fsSL https://claude.ai/install.sh | bash && cp -L ~/.local/bin/claude /usr/local/bin/claude && rm -rf ~/.local/share/claude ~/.local/bin/claude
COPY --from=builder /loop /usr/local/bin/loop
COPY --from=builder /go/bin/tk /usr/local/bin/tk
COPY --from=builder /go/bin/gopls /usr/local/bin/gopls
COPY internal/container/image/setup.sh .loop/setup.s[h] /usr/local/bin/
RUN --mount=type=secret,id=gitconfig,target=/root/.gitconfig chmod +x /usr/local/bin/setup.sh && /usr/local/bin/setup.sh
COPY internal/container/image/entrypoint.sh /usr/local/bin/entrypoint.sh
COPY internal/container/image/agent-bashrc /etc/loop/bashrc
RUN chmod +x /usr/local/bin/entrypoint.sh
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["claude"]
