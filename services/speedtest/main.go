// speedtest: Ookla's CLI on a fixed schedule, its last result served as Prometheus metrics and
// every result appended to a JSON-lines history. A restart serves the history's last line rather
// than testing on start, which would mean one test per deploy.
package main

import (
	"bufio"
	"bytes"
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

var historyPath = env("HISTORY_PATH", "/state/results.jsonl")

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
	// Held for a whole test, so a manual run and a scheduled one never share the line.
	testRun sync.Mutex
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

// compact drops serverSelection, the servers tried before picking one: over half of each result.
func compact(raw []byte) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	delete(fields, "serverSelection")
	return json.Marshal(fields)
}

func run() (*result, error) {
	testRun.Lock()
	defer testRun.Unlock()

	out, err := exec.Command("speedtest", "--accept-license", "--accept-gdpr", "--format=json").Output()
	var parsed result
	if err == nil {
		err = json.Unmarshal(out, &parsed)
	}
	if err == nil && parsed.Download.Bandwidth == 0 {
		err = fmt.Errorf("no download measured: %s", strings.TrimSpace(string(out)))
	}
	var line []byte
	if err == nil {
		line, err = compact(out)
	}

	mu.Lock()
	defer mu.Unlock()
	lastTry = time.Now()
	if err != nil {
		failures++
		log.Printf("speedtest failed: %v", err)
		return nil, err
	}
	last, lastRaw = &parsed, line
	if err := appendLine(line); err != nil {
		log.Printf("cannot keep the result: %v", err)
	}
	log.Printf("down %.0f Mbit/s, up %.0f Mbit/s, ping %.1f ms via %s, %s",
		parsed.Download.Bandwidth*8/1e6, parsed.Upload.Bandwidth*8/1e6, parsed.Ping.Latency,
		parsed.Server.Name, parsed.Result.URL)
	return &parsed, nil
}

func appendLine(line []byte) error {
	file, err := os.OpenFile(historyPath, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	// A line cut short by a crash has no newline; without one here the next result joins it.
	tail := make([]byte, 1)
	if info, err := file.Stat(); err == nil && info.Size() > 0 {
		if _, err := file.ReadAt(tail, info.Size()-1); err == nil && tail[0] != '\n' {
			line = append([]byte{'\n'}, line...)
		}
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

// load serves the newest valid line, so a line cut short by a crash mid-write costs only itself.
func load() {
	file, err := os.Open(historyPath)
	if err != nil {
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		var parsed result
		if len(line) == 0 || json.Unmarshal(line, &parsed) != nil {
			continue
		}
		last, lastRaw = &parsed, bytes.Clone(line)
	}
	if err := scanner.Err(); err != nil {
		log.Printf("reading %s: %v", historyPath, err)
	}
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
		// One new series per test through result_url: four a day, and it is what makes the result linkable.
		gauge("speedtest_server_info", "Which server and ISP the result came from, and its page on speedtest.net")
		fmt.Fprintf(&out, "speedtest_server_info{server_id=\"%d\",server=%q,location=%q,isp=%q,result_url=%q} 1\n",
			r.Server.ID, r.Server.Name, r.Server.Location, r.ISP, r.Result.URL)
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

func runNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST to run a test", http.StatusMethodNotAllowed)
		return
	}
	if _, err := run(); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	lastResult(w, r)
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
	http.HandleFunc("/run", runNow)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
