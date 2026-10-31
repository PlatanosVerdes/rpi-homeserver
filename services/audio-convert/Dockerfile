ARG GO_IMAGE
ARG ALPINE_VERSION

FROM golang:${GO_IMAGE} AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY *.go icon.svg ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /audio-convert .

# iOS ignores SVG icons on the home screen, so the PNG is rendered from the same icon.svg, its
# background squared because iOS rounds the corners itself.
FROM alpine:${ALPINE_VERSION} AS icon
RUN apk add --no-cache rsvg-convert
COPY icon.svg /
RUN echo 'svg > rect:first-of-type { rx: 0; ry: 0 }' > /square.css \
    && rsvg-convert --width 180 --height 180 --stylesheet /square.css /icon.svg -o /apple-touch-icon.png

# python3 and ffmpeg run scripts/sync/audio-compat.py, which compose mounts from the repo rather
# than copying it in, so the page and the night run always execute the same file.
FROM alpine:${ALPINE_VERSION}
ARG FFMPEG_VERSION
ARG PYTHON_VERSION
RUN apk add --no-cache ca-certificates tzdata "ffmpeg=~${FFMPEG_VERSION}" "python3=~${PYTHON_VERSION}"
COPY --from=builder /audio-convert /usr/local/bin/audio-convert
COPY --from=icon /apple-touch-icon.png /usr/local/share/audio-convert/apple-touch-icon.png
ENTRYPOINT ["audio-convert"]
