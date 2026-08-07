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
	token        string
	margin       int
	sonarrURL    string
	sonarrAPIKey string
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
	return config{
		token:        mustEnv("WATCH_NEXT_TOKEN"),
		margin:       margin,
		sonarrURL:    sonarrURL,
		sonarrAPIKey: key,
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
// series after the one just watched (season/episode as reported by the watched hook).
func handleWatched(cfg config, tvdbID, season, episodeNum int) error {
	seriesBody, err := sonarrRequest(cfg, "GET", fmt.Sprintf("/api/v3/series?tvdbId=%d", tvdbID), nil)
	if err != nil {
		return fmt.Errorf("series lookup: %w", err)
	}
	var seriesList []map[string]interface{}
	if err := json.Unmarshal(seriesBody, &seriesList); err != nil {
		return fmt.Errorf("series lookup: bad json: %w", err)
	}
	if len(seriesList) == 0 {
		return fmt.Errorf("no series found for tvdbId %d", tvdbID)
	}
	seriesID := toInt(seriesList[0]["id"])
	title, _ := seriesList[0]["title"].(string)

	epsBody, err := sonarrRequest(cfg, "GET", fmt.Sprintf("/api/v3/episode?seriesId=%d", seriesID), nil)
	if err != nil {
		return fmt.Errorf("%s: episode list: %w", title, err)
	}
	var episodes []map[string]interface{}
	if err := json.Unmarshal(epsBody, &episodes); err != nil {
		return fmt.Errorf("%s: episode list: bad json: %w", title, err)
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
		return fmt.Errorf("%s: S%02dE%02d not found among its episodes", title, season, episodeNum)
	}

	var toSearch []int
	monitoredCount := 0
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
			return fmt.Errorf("%s: search command: %w", title, err)
		}
	}

	log.Printf("%s S%02dE%02d watched: monitored %d, searching %d", title, season, episodeNum, monitoredCount, len(toSearch))
	return nil
}

func checkToken(r *http.Request, token string) bool {
	got := r.URL.Query().Get("token")
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

func tautulliHandler(cfg config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !checkToken(r, cfg.token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var payload struct {
			TVDBID  string `json:"tvdb_id"`
			Season  int    `json:"season"`
			Episode int    `json:"episode"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&payload); err != nil {
			log.Printf("tautulli: bad payload: %v", err)
			w.WriteHeader(http.StatusOK)
			return
		}
		tvdbID, err := strconv.Atoi(strings.TrimSpace(payload.TVDBID))
		if err != nil || tvdbID == 0 {
			log.Printf("tautulli: missing or invalid tvdb_id in payload")
			w.WriteHeader(http.StatusOK)
			return
		}
		if err := handleWatched(cfg, tvdbID, payload.Season, payload.Episode); err != nil {
			log.Printf("tautulli: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}
}

func jellyfinHandler(cfg config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !checkToken(r, cfg.token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var payload struct {
			ItemType           string `json:"ItemType"`
			PlayedToCompletion bool   `json:"PlayedToCompletion"`
			SeasonNumber       int    `json:"SeasonNumber"`
			EpisodeNumber      int    `json:"EpisodeNumber"`
			ProviderIds        struct {
				Tvdb string `json:"Tvdb"`
			} `json:"ProviderIds"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&payload); err != nil {
			log.Printf("jellyfin: bad payload: %v", err)
			w.WriteHeader(http.StatusOK)
			return
		}
		if payload.ItemType != "Episode" || !payload.PlayedToCompletion {
			w.WriteHeader(http.StatusOK)
			return
		}
		tvdbID, err := strconv.Atoi(strings.TrimSpace(payload.ProviderIds.Tvdb))
		if err != nil || tvdbID == 0 {
			log.Printf("jellyfin: missing or invalid ProviderIds.Tvdb in payload")
			w.WriteHeader(http.StatusOK)
			return
		}
		if err := handleWatched(cfg, tvdbID, payload.SeasonNumber, payload.EpisodeNumber); err != nil {
			log.Printf("jellyfin: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}
}

func main() {
	cfg := loadConfig()
	log.Printf("watch-next started, sonarr=%s margin=%d", cfg.sonarrURL, cfg.margin)

	mux := http.NewServeMux()
	mux.HandleFunc("/hooks/tautulli", tautulliHandler(cfg))
	mux.HandleFunc("/hooks/jellyfin", jellyfinHandler(cfg))
	log.Fatal(http.ListenAndServe(":"+listenPort, mux))
}
