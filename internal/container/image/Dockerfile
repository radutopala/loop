FROM golang:1.26-alpine AS builder
RUN apk add --no-cache git
ENV GOTOOLCHAIN=auto
RUN go install golang.org/x/tools/gopls@latest

FROM golang:1.26-alpine
ENV GOTOOLCHAIN=auto
RUN apk add --no-cache bash ca-certificates curl git make nodejs npm docker-cli docker-cli-compose su-exec aws-cli libgcc libstdc++ ripgrep uv gcc musl-dev openssh-client
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
RUN chmod +x /usr/local/bin/entrypoint.sh
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["claude"]
