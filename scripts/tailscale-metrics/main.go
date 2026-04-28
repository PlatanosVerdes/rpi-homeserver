package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	outputFile = "/var/lib/node_exporter/textfile_collector/tailscale.prom"
	envFile    = "/home/raspi/rpi-homeserver/.env"
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

func loadEnvKey(path, key string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	prefix := key + "="
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, prefix) {
			val := strings.TrimPrefix(line, prefix)
			return strings.Trim(val, `"'`)
		}
	}
	return ""
}

func getStatus() (*tailscaleStatus, error) {
	out, err := exec.Command("tailscale", "status", "--json").Output()
	if err != nil {
		return nil, err
	}
	var s tailscaleStatus
	return &s, json.Unmarshal(out, &s)
}

func getExitNodeProviders(apiKey string) (map[string]bool, error) {
	providers := make(map[string]bool)
	if apiKey == "" {
		return providers, nil
	}
	req, err := http.NewRequest("GET", "https://api.tailscale.com/api/v2/tailnet/-/devices?fields=all", nil)
	if err != nil {
		return providers, err
	}
	req.SetBasicAuth(apiKey, "")
	// Use Google DNS directly — system DNS may be Tailscale MagicDNS which can fail for external domains
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

func peerHostname(p peer) string {
	if p.DNSName != "" {
		trimmed := strings.TrimSuffix(p.DNSName, ".")
		return strings.SplitN(trimmed, ".", 2)[0]
	}
	return p.HostName
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func main() {
	status, err := getStatus()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: tailscale status: %v\n", err)
		os.Exit(1)
	}

	providers, err := getExitNodeProviders(loadEnvKey(envFile, "TAILSCALE_API_KEY"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: tailscale API: %v\n", err)
	}

	tmp := outputFile + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	w := bufio.NewWriter(f)
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

	w.Flush()
	f.Close()

	if err := os.Rename(tmp, outputFile); err != nil {
		fmt.Fprintf(os.Stderr, "error: rename: %v\n", err)
		os.Exit(1)
	}
}
