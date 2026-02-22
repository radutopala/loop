FROM golang:1.26-alpine AS builder
ENV GOTOOLCHAIN=auto
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /loop ./cmd/loop
RUN go install github.com/radutopala/ticket/cmd/tk@latest
RUN go install golang.org/x/tools/gopls@latest

FROM golang:1.26-alpine
ENV GOTOOLCHAIN=auto
RUN apk add --no-cache bash ca-certificates curl git make nodejs npm docker-cli docker-cli-compose su-exec aws-cli libgcc libstdc++ ripgrep uv gcc musl-dev openssh-client
ENV USE_BUILTIN_RIPGREP=0
ARG CLAUDE_VERSION=latest
RUN curl -fsSL https://claude.ai/install.sh | bash && cp -L ~/.local/bin/claude /usr/local/bin/claude && rm -rf ~/.local/share/claude ~/.local/bin/claude
COPY --from=builder /loop /usr/local/bin/loop
COPY --from=builder /go/bin/tk /usr/local/bin/tk
COPY --from=builder /go/bin/gopls /usr/local/bin/gopls
COPY internal/container/image/setup.sh .loop/setup.s[h] /usr/local/bin/
RUN --mount=type=secret,id=gitconfig,target=/root/.gitconfig chmod +x /usr/local/bin/setup.sh && /usr/local/bin/setup.sh
COPY internal/container/image/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["claude"]
