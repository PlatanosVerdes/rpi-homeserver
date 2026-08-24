FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /tracker-control .

FROM alpine:latest
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /tracker-control /usr/local/bin/tracker-control
EXPOSE 9111
ENTRYPOINT ["tracker-control"]
