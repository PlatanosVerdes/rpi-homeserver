ARG GO_IMAGE
ARG ALPINE_VERSION

FROM golang:${GO_IMAGE} AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /audio-convert .

# python3 and ffmpeg run scripts/sync/audio-compat.py, which compose mounts from the repo rather
# than copying it in, so the page and the night run always execute the same file.
FROM alpine:${ALPINE_VERSION}
ARG FFMPEG_VERSION
ARG PYTHON_VERSION
RUN apk add --no-cache ca-certificates tzdata "ffmpeg=~${FFMPEG_VERSION}" "python3=~${PYTHON_VERSION}"
COPY --from=builder /audio-convert /usr/local/bin/audio-convert
ENTRYPOINT ["audio-convert"]
