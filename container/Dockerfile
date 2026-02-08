FROM golang:1.25-alpine AS builder
ENV GOTOOLCHAIN=auto
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /loop ./cmd/loop
RUN go install github.com/radutopala/ticket/cmd/tk@latest

FROM golang:1.25-alpine
ENV GOTOOLCHAIN=auto
RUN apk add --no-cache bash ca-certificates curl git nodejs npm docker-cli su-exec
RUN npm install -g @anthropic-ai/claude-code
COPY --from=builder /loop /usr/local/bin/loop
COPY --from=builder /go/bin/tk /usr/local/bin/tk
COPY container/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
RUN adduser -D -h /home/agent agent \
    && (addgroup -S docker 2>/dev/null || true) \
    && adduser agent docker
RUN mkdir -p /home/agent/.claude && chown -R agent:agent /home/agent/.claude
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
