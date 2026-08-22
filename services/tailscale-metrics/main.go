// Tailscale peer metrics as a Prometheus exporter.
//
// Two sources, because neither has the whole picture:
//
//	tailscaled's LocalAPI over its unix socket   who is online, bytes per peer, which exit node
//	                                             THIS device is routing through
//	the Tailscale control API (needs an API key) which peers are *approved* exit node providers
//
// The socket is used directly rather than shelling out to `tailscale status --json`: it is the
// same JSON, and it keeps the image at alpine plus one binary instead of pulling in the CLI.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	socketPath  = "/var/run/tailscale/tailscaled.sock"
	localAPIURL = "http://local-tailscaled.sock/localapi/v0/status"
	// Prometheus scrapes every 15s; the control API is rate limited and its answer (who is
	// *allowed* to be an exit node) changes when someone edits the admin console, not per scrape.
	providersTTL = 5 * time.Minute
)

type tailscaleStatus struct {
	Peer map[string]peer `json:"Peer"`
}

type peer struct {
	HostName     string   `json:"HostName"`
	DNSName      string   `json:"DNSName"`
	OS           string   `json:"OS"`
	TailscaleIPs []string `json:"TailscaleIPs"`
	Online       bool     `json:"Online"`
	RxBytes      int64    `json:"RxBytes"`
	TxBytes      int64    `json:"TxBytes"`
	ExitNode     bool     `json:"ExitNode"`
}

type apiResponse struct {
	Devices []apiDevice `json:"devices"`
}

type apiDevice struct {
	Name          string   `json:"name"`
	EnabledRoutes []string `json:"enabledRoutes"`
}

var localClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	},
}

func getStatus() (*tailscaleStatus, error) {
	resp, err := localClient.Get(localAPIURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("localapi returned %s", resp.Status)
	}
	var s tailscaleStatus
	return &s, json.NewDecoder(resp.Body).Decode(&s)
}

func fetchExitNodeProviders(apiKey string) (map[string]bool, error) {
	providers := make(map[string]bool)
	if apiKey == "" {
		return providers, nil
	}
	req, err := http.NewRequest("GET", "https://api.tailscale.com/api/v2/tailnet/-/devices?fields=all", nil)
	if err != nil {
		return providers, err
	}
	req.SetBasicAuth(apiKey, "")
	// Google DNS directly: the container's resolver may be MagicDNS, which can fail for
	// external domains.
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, "8.8.8.8:53")
		},
	}
	transport := &http.Transport{DialContext: (&net.Dialer{Resolver: resolver}).DialContext}
	resp, err := (&http.Client{Timeout: 10 * time.Second, Transport: transport}).Do(req)
	if err != nil {
		return providers, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return providers, err
	}
	var ar apiResponse
	if err := json.Unmarshal(body, &ar); err != nil {
		return providers, err
	}
	for _, dev := range ar.Devices {
		name := strings.SplitN(dev.Name, ".", 2)[0]
		for _, route := range dev.EnabledRoutes {
			if route == "0.0.0.0/0" || route == "::/0" {
				providers[name] = true
				break
			}
		}
	}
	return providers, nil
}

type providerCache struct {
	mu      sync.Mutex
	apiKey  string
	value   map[string]bool
	fetched time.Time
}

// get returns the last good answer whenever the API is unreachable or the TTL has not expired.
// A control-API outage should blank one gauge at worst, never fail the scrape.
func (c *providerCache) get() map[string]bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.value != nil && time.Since(c.fetched) < providersTTL {
		return c.value
	}
	v, err := fetchExitNodeProviders(c.apiKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: tailscale API: %v\n", err)
		if c.value != nil {
			return c.value
		}
		return map[string]bool{}
	}
	c.value, c.fetched = v, time.Now()
	return v
}

func peerHostname(p peer) string {
	if p.DNSName != "" {
		return strings.SplitN(strings.TrimSuffix(p.DNSName, "."), ".", 2)[0]
	}
	return p.HostName
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func writeMetrics(w io.Writer, status *tailscaleStatus, providers map[string]bool) {
	fmt.Fprintln(w, "# HELP tailscale_peer_online 1 if peer is currently reachable, 0 if offline")
	fmt.Fprintln(w, "# TYPE tailscale_peer_online gauge")
	fmt.Fprintln(w, "# HELP tailscale_peer_rx_bytes Total bytes received from peer")
	fmt.Fprintln(w, "# TYPE tailscale_peer_rx_bytes counter")
	fmt.Fprintln(w, "# HELP tailscale_peer_tx_bytes Total bytes sent to peer")
	fmt.Fprintln(w, "# TYPE tailscale_peer_tx_bytes counter")
	fmt.Fprintln(w, "# HELP tailscale_peer_is_exit_node 1 if peer is an approved exit node provider")
	fmt.Fprintln(w, "# TYPE tailscale_peer_is_exit_node gauge")
	fmt.Fprintln(w, "# HELP tailscale_peer_is_active_exit_node 1 if THIS device routes through this peer as exit node")
	fmt.Fprintln(w, "# TYPE tailscale_peer_is_active_exit_node gauge")
	fmt.Fprintln(w, "# HELP tailscale_scrape_timestamp Unix timestamp of last successful scrape")
	fmt.Fprintln(w, "# TYPE tailscale_scrape_timestamp gauge")
	fmt.Fprintf(w, "tailscale_scrape_timestamp %d\n", time.Now().Unix())

	for _, p := range status.Peer {
		hostname := peerHostname(p)
		ip := ""
		if len(p.TailscaleIPs) > 0 {
			ip = p.TailscaleIPs[0]
		}
		labels := fmt.Sprintf(`hostname="%s",ip="%s",os="%s"`, hostname, ip, p.OS)
		fmt.Fprintf(w, "tailscale_peer_online{%s} %d\n", labels, boolToInt(p.Online))
		fmt.Fprintf(w, "tailscale_peer_rx_bytes{%s} %d\n", labels, p.RxBytes)
		fmt.Fprintf(w, "tailscale_peer_tx_bytes{%s} %d\n", labels, p.TxBytes)
		fmt.Fprintf(w, "tailscale_peer_is_exit_node{%s} %d\n", labels, boolToInt(providers[hostname]))
		fmt.Fprintf(w, "tailscale_peer_is_active_exit_node{%s} %d\n", labels, boolToInt(p.ExitNode))
	}
}

func main() {
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":9736"
	}
	cache := &providerCache{apiKey: os.Getenv("TAILSCALE_API_KEY")}

	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		status, err := getStatus()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: tailscale status: %v\n", err)
			http.Error(w, fmt.Sprintf("tailscale status: %v", err), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		writeMetrics(w, status, cache.get())
	})

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if _, err := getStatus(); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ok")
	})

	fmt.Fprintf(os.Stderr, "tailscale-metrics listening on %s\n", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
