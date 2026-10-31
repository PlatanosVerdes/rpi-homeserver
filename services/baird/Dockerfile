FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY *.go icon.svg ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /baird .

# iOS ignores SVG icons on the home screen, so the PNG is rendered from the same icon.svg, its
# background squared because iOS rounds the corners itself.
FROM alpine:latest AS icon
RUN apk add --no-cache rsvg-convert
COPY icon.svg /
RUN echo 'svg > rect:first-of-type { rx: 0; ry: 0 }' > /square.css \
    && rsvg-convert --width 180 --height 180 --stylesheet /square.css /icon.svg -o /apple-touch-icon.png

FROM alpine:latest
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /baird /usr/local/bin/baird
COPY --from=icon /apple-touch-icon.png /usr/local/share/baird/apple-touch-icon.png
ENTRYPOINT ["baird"]
