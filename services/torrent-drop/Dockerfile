FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY *.go icon.svg apple-touch-icon.png ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /torrent-drop .

FROM alpine:latest
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /torrent-drop /usr/local/bin/torrent-drop
ENTRYPOINT ["torrent-drop"]
