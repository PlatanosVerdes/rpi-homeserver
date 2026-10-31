FROM golang:1.27-alpine AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY *.go icon.svg apple-touch-icon.png ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /farnsworth .

FROM alpine:latest
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /farnsworth /usr/local/bin/farnsworth
ENTRYPOINT ["farnsworth"]
