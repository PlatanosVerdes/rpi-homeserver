ARG GO_IMAGE
ARG ALPINE_VERSION

FROM golang:${GO_IMAGE} AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY *.go icon.svg apple-touch-icon.png ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /audio-manager .

# python3 and ffmpeg run scripts/sync/audio-compat.py, which compose mounts from the repo rather
# than copying it in, so the page and the night run always execute the same file. numpy and
# mkvtoolnix are add-audio.py's, which aligns a dub and muxes it in; ffprobe also reads a probe's head.
FROM alpine:${ALPINE_VERSION}
ARG FFMPEG_VERSION
ARG PYTHON_VERSION
RUN apk add --no-cache ca-certificates tzdata "ffmpeg=~${FFMPEG_VERSION}" "python3=~${PYTHON_VERSION}" py3-numpy mkvtoolnix
COPY --from=builder /audio-manager /usr/local/bin/audio-manager
ENTRYPOINT ["audio-manager"]
