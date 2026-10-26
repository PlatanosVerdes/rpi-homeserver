FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /baird .

FROM alpine:latest
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /baird /usr/local/bin/baird
ENTRYPOINT ["baird"]
