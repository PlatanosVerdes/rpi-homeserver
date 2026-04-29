BINARY = tailscale-metrics
GOOS   = linux
GOARCH = arm64

build:
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -ldflags="-s -w" -o $(BINARY) .

clean:
	rm -f $(BINARY)

.PHONY: build clean