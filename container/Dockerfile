FROM golang:1.25-alpine AS builder
ENV GOTOOLCHAIN=auto
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /loop ./cmd/loop
RUN go install github.com/radutopala/ticket/cmd/tk@latest
RUN go install github.com/warrensbox/terraform-switcher@latest
RUN go install github.com/cli/cli/v2/cmd/gh@latest

FROM golang:1.25-alpine
ENV GOTOOLCHAIN=auto
RUN apk add --no-cache bash ca-certificates curl git make nodejs npm docker-cli docker-cli-compose su-exec aws-cli
RUN npm install -g @anthropic-ai/claude-code
COPY --from=builder /loop /usr/local/bin/loop
COPY --from=builder /go/bin/tk /usr/local/bin/tk
COPY --from=builder /go/bin/terraform-switcher /usr/local/bin/tfswitch
COPY --from=builder /go/bin/gh /usr/local/bin/gh
COPY internal/container/image/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["claude"]
