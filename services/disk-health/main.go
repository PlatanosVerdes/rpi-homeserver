// SMART health and spin state of every sd* disk, as a Prometheus exporter.
//
// Every poll asks smartctl with -n standby, so a sleeping disk answers that it sleeps and is never
// spun up by monitoring.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type exporter struct {
	devDir     string
	deviceType string

	mu       sync.Mutex
	states   map[string]diskState
	lastPoll time.Time
}

func (e *exporter) smartctl(disk string) []byte {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// smartctl exits non-zero for warnings it also reports in the JSON, so the output is kept.
	out, _ := exec.CommandContext(ctx, "smartctl", "--json", "-n", "standby", "-i", "-H", "-A",
		"-d", e.deviceType, filepath.Join(e.devDir, disk)).Output()
	return out
}

func (e *exporter) poll() {
	disks, err := listDisks(e.devDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: listing %s: %v\n", e.devDir, err)
		return
	}
	next := make(map[string]diskState, len(disks))
	for _, disk := range disks {
		e.mu.Lock()
		prev, ok := e.states[disk]
		e.mu.Unlock()
		if !ok {
			prev = diskState{Disk: disk}
		}
		s := update(prev, e.smartctl(disk), time.Now())
		if !s.PollOK {
			fmt.Fprintf(os.Stderr, "warning: smartctl got no power mode from %s\n", disk)
		}
		next[disk] = s
	}
	e.mu.Lock()
	e.states, e.lastPoll = next, time.Now()
	e.mu.Unlock()
}

func (e *exporter) serveMetrics(w http.ResponseWriter, _ *http.Request) {
	e.mu.Lock()
	states := make([]diskState, 0, len(e.states))
	for _, disk := range sortedKeys(e.states) {
		states = append(states, e.states[disk])
	}
	lastPoll := e.lastPoll
	e.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	writeMetrics(w, states, lastPoll)
}

func sortedKeys(m map[string]diskState) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	interval, err := time.ParseDuration(env("POLL_INTERVAL", "60s"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: POLL_INTERVAL: %v\n", err)
		os.Exit(1)
	}
	e := &exporter{
		devDir:     env("DEV_DIR", "/host-dev"),
		deviceType: env("SMARTCTL_DEVICE_TYPE", "sat"),
		states:     map[string]diskState{},
	}
	go func() {
		for {
			e.poll()
			time.Sleep(interval)
		}
	}()

	http.HandleFunc("/metrics", e.serveMetrics)
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ok") })
	addr := env("LISTEN_ADDR", ":9112")
	fmt.Fprintf(os.Stderr, "disk-health listening on %s\n", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
