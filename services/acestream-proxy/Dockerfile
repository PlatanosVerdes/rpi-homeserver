FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod .
COPY main.go .
RUN go build -o acestream-proxy .

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/acestream-proxy .
EXPOSE 6879
CMD ["./acestream-proxy"]
