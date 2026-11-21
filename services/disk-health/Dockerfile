FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /disk-health .

FROM alpine:latest
RUN apk add --no-cache ca-certificates tzdata smartmontools
COPY --from=builder /disk-health /usr/local/bin/disk-health
EXPOSE 9112
ENTRYPOINT ["disk-health"]
