FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /subtitle-links .

FROM alpine:latest
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /subtitle-links /usr/local/bin/subtitle-links
ENTRYPOINT ["subtitle-links"]
