FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /loop ./cmd/loop

FROM alpine:3.21
RUN apk add --no-cache ca-certificates nodejs npm
RUN npm install -g @anthropic-ai/claude-code
COPY --from=builder /loop /usr/local/bin/loop
COPY container/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
RUN adduser -D -h /home/agent agent
RUN mkdir -p /home/agent/.claude && chown -R agent:agent /home/agent/.claude
RUN mkdir -p /work /mcp && chown agent:agent /work /mcp
WORKDIR /work
USER agent
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
