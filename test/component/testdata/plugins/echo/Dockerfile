FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod main.go ./
RUN CGO_ENABLED=0 go build -trimpath -o /echo . && mkdir /data

FROM scratch
LABEL dev.loop.plugin=1
COPY --from=build /echo /echo
COPY --from=build --chown=65532:65532 /data /data
COPY plugin.json /loop-plugin/plugin.json
USER 65532:65532
ENTRYPOINT ["/echo"]
