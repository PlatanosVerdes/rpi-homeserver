FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /acestream-updater .

FROM alpine:latest
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /acestream-updater /usr/local/bin/acestream-updater
ENTRYPOINT ["acestream-updater"]
