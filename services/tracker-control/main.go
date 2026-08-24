// tracker-control: read what each private tracker says about the account, then move the knobs.
//
// This was two cron scripts that handed a reading to each other through a state.json, five minutes
// apart, which is why one of them had to refuse to act on a reading older than three hours. One
// process reads and acts in the same pass, so there is nothing stale to guard against, and its
// metrics are scraped rather than pushed: a pushed value outlives whatever pushed it, so a wedged
// loop looked exactly like a healthy one.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	appdata     = env("APPDATA_PATH", "/appdata")
	dataRoot    = env("DATA_ROOT", "/mnt/data")
	stateDir    = env("TRACKER_STATE", "/state")
	rulesFile   = env("TRACKER_RULES", "/config/rules.json")
	radarrURL   = env("RADARR_URL", "http://radarr:7878")
	sonarrURL   = env("SONARR_URL", "http://sonarr:8989")
	prowlarrURL = env("PROWLARR_URL", "http://prowlarr:9696")
	autobrrURL  = env("AUTOBRR_URL", "http://autobrr:7474")
	dryRun      = os.Getenv("DRY_RUN") == "1"

	client = &http.Client{Timeout: 30 * time.Second}
)

type snapshot struct {
	body     string
	last     time.Time
	took     time.Duration
	failures int
	interval time.Duration
}

var (
	mu      sync.Mutex
	current snapshot
	logs    []string
)

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// logLine goes to the container log, which is where Vector picks it up.
func logLine(text string) {
	log.Print(text)
}

func loop(every time.Duration) {
	for {
		started := time.Now()
		rules, err := loadRules()
		if err != nil {
			log.Printf("rules: %v", err)
			time.Sleep(every)
			continue
		}

		readLines, numbers, readProblems := read(rules)
		actLines, actProblems := act(rules, numbers)
		problems := append(readProblems, actProblems...)
		for _, problem := range problems {
			log.Printf("%s", problem)
		}

		mu.Lock()
		current = snapshot{
			body:     strings.Join(append(readLines, actLines...), "\n"),
			last:     started,
			took:     time.Since(started),
			failures: len(problems),
			interval: every,
		}
		mu.Unlock()
		time.Sleep(every)
	}
}

func metrics(w http.ResponseWriter, _ *http.Request) {
	mu.Lock()
	snap := current
	mu.Unlock()

	var out strings.Builder
	if snap.body != "" {
		out.WriteString(snap.body)
		out.WriteString("\n")
	}
	out.WriteString("# HELP tracker_control_loop_timestamp_seconds When the loop last finished a pass\n")
	out.WriteString("# TYPE tracker_control_loop_timestamp_seconds gauge\n")
	out.WriteString("# HELP tracker_control_loop_seconds How long that pass took\n")
	out.WriteString("# TYPE tracker_control_loop_seconds gauge\n")
	out.WriteString("# HELP tracker_control_loop_failures Things that failed in that pass\n")
	out.WriteString("# TYPE tracker_control_loop_failures gauge\n")
	out.WriteString("# HELP tracker_control_loop_interval_seconds How often it is supposed to run, so overdue is answerable without knowing the schedule\n")
	out.WriteString("# TYPE tracker_control_loop_interval_seconds gauge\n")
	out.WriteString("# HELP tracker_control_dry_run 1 while it reports what it would change and writes nothing\n")
	out.WriteString("# TYPE tracker_control_dry_run gauge\n")
	fmt.Fprintf(&out, "tracker_control_loop_timestamp_seconds %d\n", snap.last.Unix())
	fmt.Fprintf(&out, "tracker_control_loop_seconds %.3f\n", snap.took.Seconds())
	fmt.Fprintf(&out, "tracker_control_loop_failures %d\n", snap.failures)
	fmt.Fprintf(&out, "tracker_control_loop_interval_seconds %d\n", int(snap.interval.Seconds()))
	fmt.Fprintf(&out, "tracker_control_dry_run %d\n", boolInt(dryRun))

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	io.WriteString(w, out.String())
}

func main() {
	port := env("PORT", "9111")
	every := 30 * time.Minute
	if value, err := strconv.Atoi(os.Getenv("LOOP_INTERVAL")); err == nil && value > 0 {
		every = time.Duration(value) * time.Second
	}

	go loop(every)
	http.HandleFunc("/metrics", metrics)
	http.HandleFunc("/", metrics)
	log.Printf("tracker-control on :%s, every %s, dry_run=%v", port, every, dryRun)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func loadRules() (map[string]map[string]any, error) {
	raw, err := os.ReadFile(rulesFile)
	if err != nil {
		return nil, err
	}
	var all map[string]map[string]any
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, err
	}
	out := map[string]map[string]any{}
	for name, config := range all {
		if !strings.HasPrefix(name, "_") {
			out[name] = config
		}
	}
	return out, nil
}

// --- talking to everything else ---------------------------------------------------------------

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

func getJSON[T any](url, key string) (T, error) {
	var out T
	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return out, err
	}
	if key != "" {
		request.Header.Set("X-Api-Key", key)
	}
	resp, err := client.Do(request)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return out, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

func put(url, key string, body any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequest("PUT", url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("X-Api-Key", key)
	}
	resp, err := client.Do(request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return nil
}

func autobrrGet[T any](path string) (T, error) {
	var out T
	key := os.Getenv("AUTOBRR_API_KEY")
	if key == "" {
		return out, fmt.Errorf("no AUTOBRR_API_KEY in the environment")
	}
	request, err := http.NewRequest("GET", autobrrURL+"/api/"+path, nil)
	if err != nil {
		return out, err
	}
	request.Header.Set("X-API-Token", key)
	resp, err := client.Do(request)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return out, fmt.Errorf("autobrr %s: %s", path, resp.Status)
	}
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

func autobrrPut(path string, body any) error {
	key := os.Getenv("AUTOBRR_API_KEY")
	if key == "" {
		return fmt.Errorf("no AUTOBRR_API_KEY in the environment")
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequest("PUT", autobrrURL+"/api/"+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-API-Token", key)
	resp, err := client.Do(request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("autobrr %s: %s", path, resp.Status)
	}
	return nil
}

func autobrrFilter(name string) (map[string]any, error) {
	if name == "" {
		return nil, fmt.Errorf("no autobrr filter named in the rules")
	}
	filters, err := autobrrGet[[]map[string]any]("filters")
	if err != nil {
		return nil, err
	}
	for _, filter := range filters {
		if str(filter, "name") == name {
			return filter, nil
		}
	}
	return nil, fmt.Errorf("autobrr has no filter named %s", name)
}

// A dry run is an inspection: it must not write, and a message on the phone is a write.
func telegram(text string) {
	if dryRun {
		logLine("[dry-run] telegram: " + strings.ReplaceAll(text, "\n", " | "))
		return
	}
	token, chat := os.Getenv("TELEGRAM_ALERT_BOT_TOKEN"), os.Getenv("TELEGRAM_ALERT_CHAT_ID")
	if token == "" || chat == "" {
		log.Print("no telegram credentials in the environment")
		return
	}
	form := url.Values{"chat_id": {chat}, "text": {text}, "parse_mode": {"HTML"}}
	resp, err := client.PostForm("https://api.telegram.org/bot"+token+"/sendMessage", form)
	if err != nil {
		log.Printf("telegram: %v", err)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
}

// --- reading loose JSON, the same shape every arr answers with --------------------------------

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

func numOr(m map[string]any, key string, fallback float64) float64 {
	if value, ok := m[key].(float64); ok {
		return value
	}
	return fallback
}

func hostSet(config map[string]any) map[string]bool {
	out := map[string]bool{}
	raw, _ := config["tracker_hosts"].([]any)
	for _, item := range raw {
		if host, ok := item.(string); ok {
			out[host] = true
		}
	}
	return out
}

func announceHost(torrent map[string]any) string {
	raw := str(torrent, "tracker")
	_, after, found := strings.Cut(raw, "://")
	if !found {
		after = raw
	}
	host, _, _ := strings.Cut(after, "/")
	host, _, _ = strings.Cut(host, ":")
	return host
}

func readAll(resp *http.Response) string {
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	return string(payload)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func escape(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return strings.ReplaceAll(value, "\n", " ")
}

func cut(value string, limit int) string {
	value = escape(value)
	if len(value) > limit {
		return value[:limit]
	}
	return value
}

func withDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func orQ(value string) string {
	if value == "" {
		return "?"
	}
	return value
}
