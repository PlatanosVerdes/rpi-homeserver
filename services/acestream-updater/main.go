package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// qualitySuffix strips resolution and source markers from channel names.
// e.g. "M+ Liga de Campeones 1080p **" → "M+ Liga de Campeones"
var qualitySuffix = regexp.MustCompile(`\s+(?:(?:720p|1080p)\s+)?\*+$`)

const (
	stateFile         = "/app/metrics.env"
	healthConcurrency = 3     // max parallel stream probes (Pi-friendly)
	healthBytes       = 8192  // bytes to read to confirm a stream is live
	healthTimeout     = 10    // seconds per stream probe
	sleepMin          = 60    // seconds
	sleepRange        = 241   // rand(0..240) + sleepMin = 60..300s
)

type config struct {
	outputFile     string
	sourceURLs     []string
	aceserveURL    string
	pushgatewayURL string
	jellyfinURL    string
	jellyfinAPIKey string
}

type state struct {
	successChanges        int
	successNoChanges      int
	errors                int
	totalRuns             int
	jellyfinLastRefreshTS int64 // unix timestamp of last successful Jellyfin refresh
}

type channel struct {
	name    string
	channel string // base name without quality suffix (for aggregation)
	group   string // group-title from EXTINF
	url     string // aceserve HTTP URL (acestream:// already rewritten)
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s not set", key)
	}
	return v
}

func loadConfig() config {
	var urls []string
	for _, u := range strings.Split(mustEnv("SOURCE_URLS"), ",") {
		if u = strings.TrimSpace(u); u != "" {
			urls = append(urls, u)
		}
	}
	return config{
		outputFile:     mustEnv("OUTPUT_FILE"),
		sourceURLs:     urls,
		aceserveURL:    mustEnv("ACESERVE_URL"),
		pushgatewayURL: mustEnv("PUSHGATEWAY_URL"),
		jellyfinURL:    mustEnv("JELLYFIN_URL"),
		jellyfinAPIKey: mustEnv("JELLYFIN_API_KEY"),
	}
}

func loadState() state {
	f, err := os.Open(stateFile)
	if err != nil {
		return state{}
	}
	defer f.Close()

	var s state
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var n int
		line := scanner.Text()
		var n64 int64
		if cnt, _ := fmt.Sscanf(line, "SUCCESS_CHANGES=%d", &n); cnt == 1 {
			s.successChanges = n
		} else if cnt, _ := fmt.Sscanf(line, "SUCCESS_NO_CHANGES=%d", &n); cnt == 1 {
			s.successNoChanges = n
		} else if cnt, _ := fmt.Sscanf(line, "ERRORS=%d", &n); cnt == 1 {
			s.errors = n
		} else if cnt, _ := fmt.Sscanf(line, "TOTAL_RUNS=%d", &n); cnt == 1 {
			s.totalRuns = n
		} else if cnt, _ := fmt.Sscanf(line, "JELLYFIN_LAST_REFRESH=%d", &n64); cnt == 1 {
			s.jellyfinLastRefreshTS = n64
		}
	}
	return s
}

func saveState(s state) {
	content := fmt.Sprintf(
		"SUCCESS_CHANGES=%d\nSUCCESS_NO_CHANGES=%d\nERRORS=%d\nTOTAL_RUNS=%d\nJELLYFIN_LAST_REFRESH=%d\n",
		s.successChanges, s.successNoChanges, s.errors, s.totalRuns, s.jellyfinLastRefreshTS,
	)
	if err := os.WriteFile(stateFile, []byte(content), 0644); err != nil {
		log.Printf("warning: failed to save state: %v", err)
	}
}

func downloadSource(rawURL string) ([]byte, int, error) {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return body, resp.StatusCode, err
}

// parseAndDedup parses the combined raw M3U bytes, deduplicates by acestream hash,
// and rewrites acestream:// URLs to the aceserve HTTP URL.
// It preserves the url-tvg attribute from the first source header so Jellyfin
// can load the EPG guide automatically.
func parseAndDedup(combined []byte, aceserveURL string) ([]channel, string) {
	scanner := bufio.NewScanner(bytes.NewReader(combined))
	seen := make(map[string]bool)
	var channels []channel
	var out strings.Builder

	var urlTVG string // extracted from the first #EXTM3U line in the sources
	var extinf string

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "#EXTM3U"):
			if urlTVG == "" {
				urlTVG = extractAttr(line, "url-tvg")
			}
		case strings.HasPrefix(line, "#EXTINF"):
			extinf = line
		case strings.HasPrefix(line, "acestream://"):
			hash := strings.TrimPrefix(line, "acestream://")
			if seen[hash] || extinf == "" {
				extinf = ""
				continue
			}
			seen[hash] = true
			name := channelName(extinf)
			group := extractAttr(extinf, "group-title")
			base := strings.TrimSpace(qualitySuffix.ReplaceAllString(name, ""))
			streamURL := aceserveURL + hash
			channels = append(channels, channel{name: name, channel: base, group: group, url: streamURL})
			out.WriteString(extinf + "\n" + streamURL + "\n")
			extinf = ""
		}
	}

	header := "#EXTM3U"
	if urlTVG != "" {
		header += fmt.Sprintf(` url-tvg="%s"`, urlTVG)
	}
	return channels, header + "\n" + out.String()
}

// extractAttr extracts the value of a quoted attribute from an M3U tag line.
// e.g. extractAttr(`#EXTM3U url-tvg="http://foo.xml"`, "url-tvg") → "http://foo.xml"
func extractAttr(line, attr string) string {
	prefix := attr + `="`
	idx := strings.Index(line, prefix)
	if idx == -1 {
		return ""
	}
	rest := line[idx+len(prefix):]
	end := strings.Index(rest, `"`)
	if end == -1 {
		return ""
	}
	return rest[:end]
}

func channelName(extinf string) string {
	if idx := strings.LastIndex(extinf, ","); idx != -1 {
		return strings.TrimSpace(extinf[idx+1:])
	}
	return extinf
}

// probeStream follows the aceserve 302 redirect and reads a few bytes from the
// actual stream. Returns true only if the stream delivers data within the timeout,
// meaning there are active peers serving the content right now.
func probeStream(streamURL string) bool {
	client := &http.Client{Timeout: healthTimeout * time.Second}
	resp, err := client.Get(streamURL)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	n, _ := io.CopyN(io.Discard, resp.Body, healthBytes)
	return n > 0
}

// checkHealth probes all channels concurrently and returns a name→(1|0) map.
func checkHealth(channels []channel) map[string]int {
	results := make(map[string]int, len(channels))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, healthConcurrency)

	for _, ch := range channels {
		wg.Add(1)
		go func(ch channel) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			v := 0
			if probeStream(ch.url) {
				v = 1
			}
			log.Printf("  health %s: %d", ch.name, v)
			mu.Lock()
			results[ch.name] = v
			mu.Unlock()
		}(ch)
	}
	wg.Wait()
	return results
}

func triggerJellyfinRefresh(jellyfinURL, apiKey string) int {
	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("POST",
		jellyfinURL+"/ScheduledTasks/Running/0c9ee3a88fc15547c6852205480da1fd", nil)
	req.Header.Set("X-Emby-Token", apiKey)
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func prometheusLabel(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

func hostFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return u.Host
}

// pushMetrics uses PUT so stale channel metrics are removed when channels disappear.
func pushMetrics(pushURL string, s state, channels []channel, jellyfinCode int,
	sourceHTTPCodes map[string]int, channelHealth map[string]int) {

	type meta struct{ channel, group string }
	metaOf := make(map[string]meta, len(channels))
	for _, ch := range channels {
		metaOf[ch.name] = meta{ch.channel, ch.group}
	}

	var buf bytes.Buffer
	w := func(format string, a ...any) { fmt.Fprintf(&buf, format, a...) }

	w("# HELP acestream_run_total Total executions\n# TYPE acestream_run_total counter\nacestream_run_total %d\n", s.totalRuns)
	w("# HELP acestream_updates_with_changes Updates with changes\n# TYPE acestream_updates_with_changes counter\nacestream_updates_with_changes %d\n", s.successChanges)
	w("# HELP acestream_updates_no_changes Executions without changes\n# TYPE acestream_updates_no_changes counter\nacestream_updates_no_changes %d\n", s.successNoChanges)
	w("# HELP acestream_update_errors Total errors\n# TYPE acestream_update_errors counter\nacestream_update_errors %d\n", s.errors)
	w("# HELP acestream_last_run_timestamp Last run timestamp\n# TYPE acestream_last_run_timestamp gauge\nacestream_last_run_timestamp %d\n", time.Now().Unix())
	w("# HELP acestream_unique_channels Current unique channel count\n# TYPE acestream_unique_channels gauge\nacestream_unique_channels %d\n", len(channels))
	w("# HELP acestream_jellyfin_refresh_http_code HTTP code from Jellyfin refresh API (0=not called)\n# TYPE acestream_jellyfin_refresh_http_code gauge\nacestream_jellyfin_refresh_http_code %d\n", jellyfinCode)
	w("# HELP acestream_jellyfin_last_refresh_timestamp Unix timestamp of last successful Jellyfin refresh\n# TYPE acestream_jellyfin_last_refresh_timestamp gauge\nacestream_jellyfin_last_refresh_timestamp %d\n", s.jellyfinLastRefreshTS)

	w("# HELP acestream_source_http_code HTTP response code per source URL (0=failed)\n# TYPE acestream_source_http_code gauge\n")
	for host, code := range sourceHTTPCodes {
		w("acestream_source_http_code{url=\"%s\"} %d\n", prometheusLabel(host), code)
	}

	if len(channelHealth) > 0 {
		w("# HELP acestream_channel_health 1=stream delivering bytes 0=no response within timeout\n# TYPE acestream_channel_health gauge\n")
		for name, v := range channelHealth {
			m := metaOf[name]
			w("acestream_channel_health{name=\"%s\",channel=\"%s\",group=\"%s\"} %d\n",
				prometheusLabel(name), prometheusLabel(m.channel), prometheusLabel(m.group), v)
		}
	}

	req, err := http.NewRequest("PUT", pushURL+"/metrics/job/acestream_updater", &buf)
	if err != nil {
		log.Printf("warning: metrics request build failed: %v", err)
		return
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("warning: push metrics failed: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("warning: pushgateway returned %d", resp.StatusCode)
	}
}

func run(cfg config) {
	s := loadState()
	s.totalRuns++
	log.Printf("Starting execution #%d", s.totalRuns)

	var combined bytes.Buffer
	sourceHTTPCodes := make(map[string]int)
	downloadErrors := 0

	for _, sourceURL := range cfg.sourceURLs {
		log.Printf("Downloading: %s", sourceURL)
		data, code, err := downloadSource(sourceURL)
		sourceHTTPCodes[hostFromURL(sourceURL)] = code

		if err != nil || code != 200 {
			log.Printf("  warning: failed (HTTP %d): %v", code, err)
			s.errors++
			downloadErrors++
			continue
		}
		combined.Write(data)
		log.Printf("  ok (%d)", code)
	}

	if downloadErrors == len(cfg.sourceURLs) {
		log.Printf("error: all sources failed")
		saveState(s)
		pushMetrics(cfg.pushgatewayURL, s, nil, 0, sourceHTTPCodes, nil)
		return
	}

	channels, newM3U := parseAndDedup(combined.Bytes(), cfg.aceserveURL)
	log.Printf("Channels: %d unique after dedup", len(channels))

	jellyfinCode := 0
	existing, err := os.ReadFile(cfg.outputFile)
	if err != nil || string(existing) != newM3U {
		if err := os.WriteFile(cfg.outputFile, []byte(newM3U), 0644); err != nil {
			log.Printf("error writing output: %v", err)
		} else {
			log.Printf("Changes applied.")
			s.successChanges++
			jellyfinCode = triggerJellyfinRefresh(cfg.jellyfinURL, cfg.jellyfinAPIKey)
			log.Printf("Jellyfin refresh: %d", jellyfinCode)
			if jellyfinCode == 204 {
				s.jellyfinLastRefreshTS = time.Now().Unix()
			}
		}
	} else {
		log.Printf("No changes detected.")
		s.successNoChanges++
	}

	log.Printf("Checking channel health (%d channels, %d concurrent)...", len(channels), healthConcurrency)
	health := checkHealth(channels)

	saveState(s)
	pushMetrics(cfg.pushgatewayURL, s, channels, jellyfinCode, sourceHTTPCodes, health)
	log.Printf("Done.")
}

func main() {
	cfg := loadConfig()
	log.Printf("Acestream updater started.")

	for {
		run(cfg)
		sleep := sleepMin + rand.Intn(sleepRange) // 60–300 seconds
		log.Printf("Sleeping %dm%ds...", sleep/60, sleep%60)
		time.Sleep(time.Duration(sleep) * time.Second)
	}
}
