FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git
ENV GOTOOLCHAIN=auto
RUN go install github.com/radutopala/loop/cmd/loop@latest
RUN go install github.com/radutopala/ticket/cmd/tk@latest
RUN go install github.com/warrensbox/terraform-switcher@latest

FROM golang:1.25-alpine
ENV GOTOOLCHAIN=auto
RUN apk add --no-cache bash ca-certificates curl git make nodejs npm docker-cli docker-cli-compose su-exec aws-cli
RUN npm install -g @anthropic-ai/claude-code
COPY --from=builder /go/bin/loop /usr/local/bin/loop
COPY --from=builder /go/bin/tk /usr/local/bin/tk
COPY --from=builder /go/bin/terraform-switcher /usr/local/bin/tfswitch
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
RUN adduser -D -h /home/agent agent \
    && (addgroup -S docker 2>/dev/null || true) \
    && adduser agent docker
RUN mkdir -p /home/agent/.claude && chown -R agent:agent /home/agent/.claude
ENV HOME=/home/agent
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
