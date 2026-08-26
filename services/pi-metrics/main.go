// pi-metrics: the numbers no exporter provides, served instead of pushed.
//
// Each collector runs on its own schedule in a goroutine and every scrape answers the last
// snapshot. That is not an optimisation: one media pass takes ~7s against five APIs, and collecting
// on demand would both risk Prometheus's 10s scrape timeout and ask those APIs every 15s instead of
// the every-five-minutes they need.
//
// So `up` and freshness are separate questions and both are answerable: up says the process lives,
// pi_metrics_last_success_timestamp_seconds says the data behind a panel is recent.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	appdata     = env("APPDATA_PATH", "/appdata")
	radarrURL   = env("RADARR_URL", "http://radarr:7878")
	sonarrURL   = env("SONARR_URL", "http://sonarr:8989")
	prowlarrURL = env("PROWLARR_URL", "http://prowlarr:9696")
	maintURL    = env("MAINTAINERR_URL", "http://maintainerr:6246")

	client = &http.Client{Timeout: 25 * time.Second}
)

type result struct {
	body     string
	last     time.Time
	took     time.Duration
	failures int
	interval time.Duration
}

var (
	mu    sync.Mutex
	store = map[string]result{}
)

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// collect runs one collector on its interval, forever. A collector reports what failed rather than
// returning an error: one unreachable app must not blank the other nine groups.
func collect(name string, every time.Duration, produce func() ([]string, []string)) {
	for {
		started := time.Now()
		lines, problems := produce()
		for _, problem := range problems {
			log.Printf("%s: %s", name, problem)
		}
		mu.Lock()
		previous := store[name]
		if len(lines) > 0 || previous.body == "" {
			previous.body = strings.Join(lines, "\n")
			previous.last = started
		}
		previous.took = time.Since(started)
		previous.failures = len(problems)
		previous.interval = every
		store[name] = previous
		mu.Unlock()
		time.Sleep(every)
	}
}

func metrics(w http.ResponseWriter, _ *http.Request) {
	var out strings.Builder
	mu.Lock()
	for _, name := range []string{"media", "disk"} {
		if entry, ok := store[name]; ok && entry.body != "" {
			out.WriteString(entry.body)
			out.WriteString("\n")
		}
	}
	current := map[string]result{}
	for name, entry := range store {
		current[name] = entry
	}
	mu.Unlock()

	// What qbit-manage posted after its last run, if it has posted one yet.
	if lines := qbmLines(); len(lines) > 0 {
		out.WriteString(strings.Join(lines, "\n"))
		out.WriteString("\n")
	}

	// zram is one sysfs read, so it is answered live rather than cached.
	if lines, problems := zram(); len(problems) == 0 {
		out.WriteString(strings.Join(lines, "\n"))
		out.WriteString("\n")
	} else {
		log.Printf("zram: %s", strings.Join(problems, "; "))
	}

	out.WriteString("# HELP pi_metrics_last_success_timestamp_seconds When this collector last produced a snapshot\n")
	out.WriteString("# TYPE pi_metrics_last_success_timestamp_seconds gauge\n")
	out.WriteString("# HELP pi_metrics_collection_seconds How long its last run took\n")
	out.WriteString("# TYPE pi_metrics_collection_seconds gauge\n")
	out.WriteString("# HELP pi_metrics_collection_failures Groups that failed in its last run\n")
	out.WriteString("# TYPE pi_metrics_collection_failures gauge\n")
	out.WriteString("# HELP pi_metrics_collection_interval_seconds How often it is supposed to run, so overdue is answerable without knowing the schedule\n")
	out.WriteString("# TYPE pi_metrics_collection_interval_seconds gauge\n")
	for _, name := range []string{"disk", "media"} {
		entry, ok := current[name]
		if !ok {
			continue
		}
		fmt.Fprintf(&out, "pi_metrics_last_success_timestamp_seconds{collector=%q} %d\n", name, entry.last.Unix())
		fmt.Fprintf(&out, "pi_metrics_collection_seconds{collector=%q} %.3f\n", name, entry.took.Seconds())
		fmt.Fprintf(&out, "pi_metrics_collection_failures{collector=%q} %d\n", name, entry.failures)
		fmt.Fprintf(&out, "pi_metrics_collection_interval_seconds{collector=%q} %d\n", name, int(entry.interval.Seconds()))
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	io.WriteString(w, out.String())
}

func main() {
	port := env("PORT", "9110")
	mediaEvery := seconds("MEDIA_INTERVAL", 300)
	diskEvery := seconds("DISK_INTERVAL", 3600)

	go collect("media", mediaEvery, media)
	go collect("disk", diskEvery, disk)

	qbmLoad()
	http.HandleFunc("/qbit-manage", qbmHook)
	http.HandleFunc("/metrics", metrics)
	http.HandleFunc("/", metrics)
	log.Printf("pi-metrics on :%s, media every %s, disk every %s", port, mediaEvery, diskEvery)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func seconds(name string, fallback int) time.Duration {
	if value, err := strconv.Atoi(os.Getenv(name)); err == nil && value > 0 {
		return time.Duration(value) * time.Second
	}
	return time.Duration(fallback) * time.Second
}

// --- the shape of every API answer here, without a struct per endpoint ------------------------

func apiKey(app string) (string, error) {
	raw, err := os.ReadFile(appdata + "/" + app + "/config.xml")
	if err != nil {
		return "", err
	}
	text := string(raw)
	start := strings.Index(text, "<ApiKey>")
	end := strings.Index(text, "</ApiKey>")
	if start < 0 || end < start {
		return "", fmt.Errorf("no ApiKey in %s/config.xml", app)
	}
	return text[start+len("<ApiKey>") : end], nil
}

func getJSON(url, key string, into any) error {
	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	if key != "" {
		request.Header.Set("X-Api-Key", key)
	}
	resp, err := client.Do(request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(into)
}

// list and object read an api answer that is a bare array or a {records: [...]} page.
func list(url, key string) ([]map[string]any, error) {
	var raw json.RawMessage
	if err := getJSON(url, key, &raw); err != nil {
		return nil, err
	}
	var direct []map[string]any
	if err := json.Unmarshal(raw, &direct); err == nil {
		return direct, nil
	}
	var page struct {
		Records []map[string]any `json:"records"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return nil, err
	}
	return page.Records, nil
}

func obj(m map[string]any, key string) map[string]any {
	if value, ok := m[key].(map[string]any); ok {
		return value
	}
	return map[string]any{}
}

func objs(m map[string]any, key string) []map[string]any {
	raw, _ := m[key].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if row, ok := item.(map[string]any); ok {
			out = append(out, row)
		}
	}
	return out
}

func str(m map[string]any, key string) string {
	switch value := m[key].(type) {
	case string:
		return value
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(value)
	}
	return ""
}

func num(m map[string]any, key string) float64 {
	if value, ok := m[key].(float64); ok {
		return value
	}
	return 0
}

func truthy(m map[string]any, key string) bool {
	value, _ := m[key].(bool)
	return value
}

// quality digs out the name three levels down, which is where both arrs keep it.
func quality(m map[string]any) string {
	if name := str(obj(obj(m, "quality"), "quality"), "name"); name != "" {
		return name
	}
	return "?"
}

func when(value string) (time.Time, error) {
	return time.Parse(time.RFC3339, strings.Replace(value, " ", "T", 1))
}

func escape(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return strings.ReplaceAll(value, "\n", " ")
}

// cut is escape plus the length limit every name label here carries: a release name can be 200
// characters and a label that long makes a table unreadable.
func cut(value string, limit int) string {
	value = escape(value)
	if len(value) > limit {
		return value[:limit]
	}
	return value
}
