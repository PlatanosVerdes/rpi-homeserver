// speedtest: Ookla's CLI on a fixed schedule, its last result served as Prometheus metrics.
// The result is kept on disk so a restart serves it; testing on start would mean one per deploy.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

var statePath = env("STATE_PATH", "/state/last.json")

type latency struct {
	IQM    float64 `json:"iqm"`
	Low    float64 `json:"low"`
	High   float64 `json:"high"`
	Jitter float64 `json:"jitter"`
}

type transfer struct {
	Bandwidth float64 `json:"bandwidth"` // bytes per second, not bits
	Bytes     float64 `json:"bytes"`
	Latency   latency `json:"latency"`
}

type result struct {
	Timestamp time.Time `json:"timestamp"`
	Ping      struct {
		Jitter  float64 `json:"jitter"`
		Latency float64 `json:"latency"`
		Low     float64 `json:"low"`
		High    float64 `json:"high"`
	} `json:"ping"`
	Download   transfer `json:"download"`
	Upload     transfer `json:"upload"`
	PacketLoss *float64 `json:"packetLoss"`
	ISP        string   `json:"isp"`
	Server     struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Location string `json:"location"`
	} `json:"server"`
	Result struct {
		URL string `json:"url"`
	} `json:"result"`
}

var (
	mu       sync.Mutex
	last     *result
	lastRaw  []byte
	failures int
	lastTry  time.Time
)

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func number(name string, fallback int) int {
	if value, err := strconv.Atoi(os.Getenv(name)); err == nil && value >= 0 {
		return value
	}
	return fallback
}

// next is the first slot after now: minute `minute` of every hour divisible by `everyHours`.
func next(now time.Time, everyHours, minute int) time.Time {
	t := now.Truncate(time.Hour).Add(time.Duration(minute) * time.Minute)
	for !t.After(now) || t.Hour()%everyHours != 0 {
		t = t.Add(time.Hour)
	}
	return t
}

func run() {
	out, err := exec.Command("speedtest", "--accept-license", "--accept-gdpr", "--format=json").Output()
	var parsed result
	if err == nil {
		err = json.Unmarshal(out, &parsed)
	}
	if err == nil && parsed.Download.Bandwidth == 0 {
		err = fmt.Errorf("no download measured: %s", strings.TrimSpace(string(out)))
	}

	mu.Lock()
	defer mu.Unlock()
	lastTry = time.Now()
	if err != nil {
		failures++
		log.Printf("speedtest failed: %v", err)
		return
	}
	last, lastRaw = &parsed, out
	if err := os.WriteFile(statePath, out, 0o644); err != nil {
		log.Printf("cannot keep the result: %v", err)
	}
	log.Printf("down %.0f Mbit/s, up %.0f Mbit/s, ping %.1f ms via %s, %s",
		parsed.Download.Bandwidth*8/1e6, parsed.Upload.Bandwidth*8/1e6, parsed.Ping.Latency,
		parsed.Server.Name, parsed.Result.URL)
}

func load() {
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return
	}
	var parsed result
	if err := json.Unmarshal(raw, &parsed); err != nil {
		log.Printf("ignoring %s: %v", statePath, err)
		return
	}
	last, lastRaw = &parsed, raw
}

func metrics(w http.ResponseWriter, _ *http.Request) {
	var out strings.Builder
	gauge := func(name, help string) {
		fmt.Fprintf(&out, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
	}
	mu.Lock()
	defer mu.Unlock()

	gauge("speedtest_failures", "Failed runs since the process started")
	fmt.Fprintf(&out, "speedtest_failures %d\n", failures)
	if !lastTry.IsZero() {
		gauge("speedtest_last_attempt_timestamp_seconds", "When a test last ran, whatever its outcome")
		fmt.Fprintf(&out, "speedtest_last_attempt_timestamp_seconds %d\n", lastTry.Unix())
	}
	if last != nil {
		r := last
		gauge("speedtest_timestamp_seconds", "When the result served here was measured")
		fmt.Fprintf(&out, "speedtest_timestamp_seconds %d\n", r.Timestamp.Unix())
		gauge("speedtest_download_bits_per_second", "Download bandwidth")
		fmt.Fprintf(&out, "speedtest_download_bits_per_second %.0f\n", r.Download.Bandwidth*8)
		gauge("speedtest_upload_bits_per_second", "Upload bandwidth")
		fmt.Fprintf(&out, "speedtest_upload_bits_per_second %.0f\n", r.Upload.Bandwidth*8)
		gauge("speedtest_ping_ms", "Idle latency to the test server")
		fmt.Fprintf(&out, "speedtest_ping_ms %g\n", r.Ping.Latency)
		gauge("speedtest_ping_jitter_ms", "Idle jitter to the test server")
		fmt.Fprintf(&out, "speedtest_ping_jitter_ms %g\n", r.Ping.Jitter)
		// Some servers do not measure loss; a missing value must not read as a perfect 0.
		if r.PacketLoss != nil {
			gauge("speedtest_packet_loss_percent", "Packet loss")
			fmt.Fprintf(&out, "speedtest_packet_loss_percent %g\n", *r.PacketLoss)
		}
		gauge("speedtest_loaded_latency_ms", "Latency while the link is saturated, the bufferbloat")
		for direction, t := range map[string]transfer{"download": r.Download, "upload": r.Upload} {
			for stat, value := range map[string]float64{"iqm": t.Latency.IQM, "low": t.Latency.Low, "high": t.Latency.High, "jitter": t.Latency.Jitter} {
				fmt.Fprintf(&out, "speedtest_loaded_latency_ms{direction=%q,stat=%q} %g\n", direction, stat, value)
			}
		}
		gauge("speedtest_server_info", "Which server and ISP the result came from")
		fmt.Fprintf(&out, "speedtest_server_info{server_id=\"%d\",server=%q,location=%q,isp=%q} 1\n",
			r.Server.ID, r.Server.Name, r.Server.Location, r.ISP)
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	io.WriteString(w, out.String())
}

func lastResult(w http.ResponseWriter, _ *http.Request) {
	mu.Lock()
	raw := lastRaw
	mu.Unlock()
	if raw == nil {
		http.Error(w, "no test yet", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(raw)
}

func main() {
	port := env("PORT", "9798")
	everyHours := max(number("EVERY_HOURS", 6), 1)
	minute := number("AT_MINUTE", 47) % 60

	load()
	go func() {
		for {
			slot := next(time.Now(), everyHours, minute)
			log.Printf("next test at %s", slot.Format(time.RFC3339))
			time.Sleep(time.Until(slot))
			run()
		}
	}()

	http.HandleFunc("/metrics", metrics)
	http.HandleFunc("/last", lastResult)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
