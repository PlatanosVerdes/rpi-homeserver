FROM golang:1.27-alpine AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /speedtest-exporter .

FROM alpine:latest
ARG OOKLA_VERSION=1.2.0
ARG OOKLA_SHA256=3953d231da3783e2bf8904b6dd72767c5c6e533e163d3742fd0437affa431bd3
RUN apk add --no-cache ca-certificates tzdata \
    && wget -qO /tmp/ookla.tgz "https://install.speedtest.net/app/cli/ookla-speedtest-${OOKLA_VERSION}-linux-aarch64.tgz" \
    && echo "${OOKLA_SHA256}  /tmp/ookla.tgz" | sha256sum -c - \
    && tar xzf /tmp/ookla.tgz -C /usr/local/bin speedtest \
    && rm /tmp/ookla.tgz
COPY --from=builder /speedtest-exporter /usr/local/bin/speedtest-exporter
EXPOSE 9798
ENTRYPOINT ["speedtest-exporter"]
