FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /tailscale-metrics .

FROM alpine:latest
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /tailscale-metrics /usr/local/bin/tailscale-metrics
EXPOSE 9736
ENTRYPOINT ["tailscale-metrics"]
