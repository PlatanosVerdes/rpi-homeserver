// watch-next monitors and searches the next Sonarr episode(s) when Tautulli (Plex) or
// Jellyfin reports one as watched, so a season downloads progressively instead of all at once.
package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxBody       = 1 << 20
	listenPort    = "9010"
	defaultURL    = "http://sonarr:8989"
	apiKeyPath    = "/config.xml"
	defaultMargin = 3
)

var apiKeyPattern = regexp.MustCompile(`<ApiKey>([^<]+)</ApiKey>`)

type config struct {
	token          string
	margin         int
	sonarrURL      string
	sonarrAPIKey   string
	pushgatewayURL string
}

// A bounded, timestamped event log and not running totals: this state is in memory, so a counter
// would reset on every deploy, and Pushgateway only overwrites the label combinations it is told
// about, so a stale one would sit there forever. Age and count are computed at query time.
const recentLimit = 20

type recentAction struct {
	source    string
	title     string
	season    int
	episode   int
	monitored int
	searched  int
	ts        int64
}

type recentError struct {
	source string
	reason string
	ts     int64
}

var (
	metricsMu     sync.Mutex
	recentActions []recentAction
	recentErrors  []recentError
)

func recordAction(source, title string, season, episode, monitored, searched int) {
	metricsMu.Lock()
	recentActions = append(recentActions, recentAction{source, title, season, episode, monitored, searched, time.Now().Unix()})
	if len(recentActions) > recentLimit {
		recentActions = recentActions[len(recentActions)-recentLimit:]
	}
	metricsMu.Unlock()
}

func recordError(source, reason string) {
	metricsMu.Lock()
	recentErrors = append(recentErrors, recentError{source, reason, time.Now().Unix()})
	if len(recentErrors) > recentLimit {
		recentErrors = recentErrors[len(recentErrors)-recentLimit:]
	}
	metricsMu.Unlock()
}

// pushMetrics is always called via `go`, off the request path: Tautulli/Jellyfin should not
// wait on Pushgateway to get their response back.
func pushMetrics(pushgatewayURL string) {
	metricsMu.Lock()
	var buf bytes.Buffer
	w := func(format string, a ...any) { fmt.Fprintf(&buf, format, a...) }

	w("# HELP watch_next_action_timestamp Recent watched-triggered actions (last %d)\n# TYPE watch_next_action_timestamp gauge\n", recentLimit)
	for i, a := range recentActions {
		w("watch_next_action_timestamp{idx=\"%d\",source=%q,title=%q,season=\"%d\",episode=\"%d\",monitored=\"%d\",searched=\"%d\"} %d\n",
			i, a.source, a.title, a.season, a.episode, a.monitored, a.searched, a.ts)
	}
	w("# HELP watch_next_error_timestamp Recent watched-hook calls that did not result in an action (last %d)\n# TYPE watch_next_error_timestamp gauge\n", recentLimit)
	for i, e := range recentErrors {
		w("watch_next_error_timestamp{idx=\"%d\",source=%q,reason=%q} %d\n", i, e.source, e.reason, e.ts)
	}
	metricsMu.Unlock()

	// PUT, not POST: replaces the whole job every time, so an entry that rolled out of the
	// last-`recentLimit` window (or an error that got fixed and stopped recurring) actually
	// disappears from Prometheus instead of lingering forever under its old label set.
	req, err := http.NewRequest("PUT", pushgatewayURL+"/metrics/job/watch_next", &buf)
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

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s not set", key)
	}
	return v
}

func readAPIKey(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	m := apiKeyPattern.FindSubmatch(data)
	if m == nil {
		return "", fmt.Errorf("no <ApiKey> found in %s", path)
	}
	return string(m[1]), nil
}

func loadConfig() config {
	margin := defaultMargin
	if v := os.Getenv("WATCH_NEXT_MARGIN"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			margin = n
		}
	}
	sonarrURL := os.Getenv("SONARR_URL")
	if sonarrURL == "" {
		sonarrURL = defaultURL
	}
	key, err := readAPIKey(apiKeyPath)
	if err != nil {
		log.Fatalf("could not read Sonarr API key from %s: %v", apiKeyPath, err)
	}
	pushgatewayURL := os.Getenv("PUSHGATEWAY_URL")
	if pushgatewayURL == "" {
		pushgatewayURL = "http://pushgateway:9091"
	}
	return config{
		token:          mustEnv("WATCH_NEXT_TOKEN"),
		margin:         margin,
		sonarrURL:      sonarrURL,
		sonarrAPIKey:   key,
		pushgatewayURL: pushgatewayURL,
	}
}

func toInt(v interface{}) int {
	if n, ok := v.(float64); ok {
		return int(n)
	}
	return 0
}

func toBool(v interface{}) bool {
	b, _ := v.(bool)
	return b
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		b = b[:n]
	}
	return string(b)
}

func sonarrRequest(cfg config, method, path string, body []byte) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, cfg.sonarrURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", cfg.sonarrAPIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return respBody, fmt.Errorf("sonarr %s %s -> %d: %s", method, path, resp.StatusCode, truncate(respBody, 300))
	}
	return respBody, nil
}

// handleWatched monitors and, if needed, searches the next cfg.margin episodes in the same
// series after the one just watched (season/episode as reported by the watched hook). Returns
// the series title and how many episodes were newly monitored/searched, for the caller to record.
func handleWatched(cfg config, tvdbID, season, episodeNum int) (title string, monitoredCount, searchedCount int, err error) {
	seriesBody, err := sonarrRequest(cfg, "GET", fmt.Sprintf("/api/v3/series?tvdbId=%d", tvdbID), nil)
	if err != nil {
		return "", 0, 0, fmt.Errorf("series lookup: %w", err)
	}
	var seriesList []map[string]interface{}
	if err := json.Unmarshal(seriesBody, &seriesList); err != nil {
		return "", 0, 0, fmt.Errorf("series lookup: bad json: %w", err)
	}
	if len(seriesList) == 0 {
		return "", 0, 0, fmt.Errorf("no series found for tvdbId %d", tvdbID)
	}
	seriesID := toInt(seriesList[0]["id"])
	title, _ = seriesList[0]["title"].(string)

	epsBody, err := sonarrRequest(cfg, "GET", fmt.Sprintf("/api/v3/episode?seriesId=%d", seriesID), nil)
	if err != nil {
		return title, 0, 0, fmt.Errorf("%s: episode list: %w", title, err)
	}
	var episodes []map[string]interface{}
	if err := json.Unmarshal(epsBody, &episodes); err != nil {
		return title, 0, 0, fmt.Errorf("%s: episode list: bad json: %w", title, err)
	}

	// Drop specials (season 0) and sort as one flat run, so the margin rolls into the next
	// season naturally instead of stopping at the season boundary.
	var real []map[string]interface{}
	for _, ep := range episodes {
		if toInt(ep["seasonNumber"]) >= 1 {
			real = append(real, ep)
		}
	}
	sort.Slice(real, func(i, j int) bool {
		si, sj := toInt(real[i]["seasonNumber"]), toInt(real[j]["seasonNumber"])
		if si != sj {
			return si < sj
		}
		return toInt(real[i]["episodeNumber"]) < toInt(real[j]["episodeNumber"])
	})

	idx := -1
	for i, ep := range real {
		if toInt(ep["seasonNumber"]) == season && toInt(ep["episodeNumber"]) == episodeNum {
			idx = i
			break
		}
	}
	if idx == -1 {
		return title, 0, 0, fmt.Errorf("%s: S%02dE%02d not found among its episodes", title, season, episodeNum)
	}

	var toSearch []int
	for i := idx + 1; i < len(real) && i <= idx+cfg.margin; i++ {
		next := real[i]
		id := toInt(next["id"])
		if !toBool(next["monitored"]) {
			next["monitored"] = true
			body, _ := json.Marshal(next)
			if _, err := sonarrRequest(cfg, "PUT", fmt.Sprintf("/api/v3/episode/%d", id), body); err != nil {
				log.Printf("%s: could not monitor episode id %d: %v", title, id, err)
				continue
			}
			monitoredCount++
		}
		if !toBool(next["hasFile"]) {
			toSearch = append(toSearch, id)
		}
	}

	if len(toSearch) > 0 {
		cmdBody, _ := json.Marshal(map[string]interface{}{"name": "EpisodeSearch", "episodeIds": toSearch})
		if _, err := sonarrRequest(cfg, "POST", "/api/v3/command", cmdBody); err != nil {
			return title, monitoredCount, 0, fmt.Errorf("%s: search command: %w", title, err)
		}
	}

	log.Printf("%s S%02dE%02d watched: monitored %d, searching %d", title, season, episodeNum, monitoredCount, len(toSearch))
	return title, monitoredCount, len(toSearch), nil
}

func checkToken(r *http.Request, token string) bool {
	got := r.URL.Query().Get("token")
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

func tautulliHandler(cfg config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !checkToken(r, cfg.token) {
			recordError("tautulli", "unauthorized")
			go pushMetrics(cfg.pushgatewayURL)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var payload struct {
			TVDBID  string `json:"tvdb_id"`
			Season  string `json:"season"`
			Episode string `json:"episode"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&payload); err != nil {
			log.Printf("tautulli: bad payload: %v", err)
			recordError("tautulli", "bad_payload")
			go pushMetrics(cfg.pushgatewayURL)
			w.WriteHeader(http.StatusOK)
			return
		}
		tvdbID, err := strconv.Atoi(strings.TrimSpace(payload.TVDBID))
		if err != nil || tvdbID == 0 {
			log.Printf("tautulli: missing or invalid tvdb_id in payload")
			recordError("tautulli", "missing_tvdb_id")
			go pushMetrics(cfg.pushgatewayURL)
			w.WriteHeader(http.StatusOK)
			return
		}
		season, sErr := strconv.Atoi(strings.TrimSpace(payload.Season))
		episode, eErr := strconv.Atoi(strings.TrimSpace(payload.Episode))
		if sErr != nil || eErr != nil {
			log.Printf("tautulli: missing or invalid season/episode in payload")
			recordError("tautulli", "bad_payload")
			go pushMetrics(cfg.pushgatewayURL)
			w.WriteHeader(http.StatusOK)
			return
		}
		title, monitored, searched, err := handleWatched(cfg, tvdbID, season, episode)
		if err != nil {
			log.Printf("tautulli: %v", err)
			recordError("tautulli", "handle_failed")
		} else {
			recordAction("tautulli", title, season, episode, monitored, searched)
		}
		go pushMetrics(cfg.pushgatewayURL)
		w.WriteHeader(http.StatusOK)
	}
}

func jellyfinHandler(cfg config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !checkToken(r, cfg.token) {
			recordError("jellyfin", "unauthorized")
			go pushMetrics(cfg.pushgatewayURL)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var payload struct {
			ItemType           string `json:"ItemType"`
			PlayedToCompletion bool   `json:"PlayedToCompletion"`
			SeasonNumber       int    `json:"SeasonNumber"`
			EpisodeNumber      int    `json:"EpisodeNumber"`
			ProviderTvdb       string `json:"Provider_tvdb"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&payload); err != nil {
			log.Printf("jellyfin: bad payload: %v", err)
			recordError("jellyfin", "bad_payload")
			go pushMetrics(cfg.pushgatewayURL)
			w.WriteHeader(http.StatusOK)
			return
		}
		if payload.ItemType != "Episode" || !payload.PlayedToCompletion {
			// Not an error, just something we don't act on (e.g. a movie, or a partial watch).
			w.WriteHeader(http.StatusOK)
			return
		}
		tvdbID, err := strconv.Atoi(strings.TrimSpace(payload.ProviderTvdb))
		if err != nil || tvdbID == 0 {
			log.Printf("jellyfin: missing or invalid Provider_tvdb in payload")
			recordError("jellyfin", "missing_tvdb_id")
			go pushMetrics(cfg.pushgatewayURL)
			w.WriteHeader(http.StatusOK)
			return
		}
		title, monitored, searched, err := handleWatched(cfg, tvdbID, payload.SeasonNumber, payload.EpisodeNumber)
		if err != nil {
			log.Printf("jellyfin: %v", err)
			recordError("jellyfin", "handle_failed")
		} else {
			recordAction("jellyfin", title, payload.SeasonNumber, payload.EpisodeNumber, monitored, searched)
		}
		go pushMetrics(cfg.pushgatewayURL)
		w.WriteHeader(http.StatusOK)
	}
}

func main() {
	cfg := loadConfig()
	log.Printf("watch-next started, sonarr=%s margin=%d", cfg.sonarrURL, cfg.margin)
	go pushMetrics(cfg.pushgatewayURL)

	mux := http.NewServeMux()
	mux.HandleFunc("/hooks/tautulli", tautulliHandler(cfg))
	mux.HandleFunc("/hooks/jellyfin", jellyfinHandler(cfg))
	log.Fatal(http.ListenAndServe(":"+listenPort, mux))
}
