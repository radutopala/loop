FROM golang:1.26-alpine AS builder
RUN apk add --no-cache git
ENV GOTOOLCHAIN=auto
RUN go install github.com/radutopala/loop/cmd/loop@latest
RUN go install github.com/radutopala/ticket/cmd/tk@latest

FROM golang:1.26-alpine
ENV GOTOOLCHAIN=auto
RUN apk add --no-cache bash ca-certificates curl git make nodejs npm docker-cli docker-cli-compose su-exec aws-cli libgcc libstdc++ ripgrep uv
ENV USE_BUILTIN_RIPGREP=0
RUN curl -fsSL https://claude.ai/install.sh | bash && cp -L ~/.local/bin/claude /usr/local/bin/claude && rm -rf ~/.local/share/claude ~/.local/bin/claude
COPY --from=builder /go/bin/loop /usr/local/bin/loop
COPY --from=builder /go/bin/tk /usr/local/bin/tk
COPY setup.sh /usr/local/bin/setup.sh
RUN chmod +x /usr/local/bin/setup.sh && /usr/local/bin/setup.sh
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["claude"]
