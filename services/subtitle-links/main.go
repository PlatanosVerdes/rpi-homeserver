// subtitle-links serves a small page listing every movie/episode that has an external
// (Bazarr-downloaded) .srt subtitle, with a direct download link per language (and a
// download-all-as-zip option per series), so subtitles can be grabbed by hand onto a device
// (e.g. to load into VLC alongside an offline-downloaded video) without hand-building URLs.
package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const listenPort = "8085"

type config struct {
	jellyfinURL string
	apiKey      string
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s not set", key)
	}
	return v
}

func loadConfig() config {
	jellyfinURL := os.Getenv("JELLYFIN_URL")
	if jellyfinURL == "" {
		jellyfinURL = "http://jellyfin:8096"
	}
	return config{jellyfinURL: jellyfinURL, apiKey: mustEnv("JELLYFIN_API_KEY")}
}

type subtitle struct {
	Index int    `json:"index"`
	Lang  string `json:"lang"`
}

type movie struct {
	ID    string     `json:"id"`
	Title string     `json:"title"`
	Subs  []subtitle `json:"subs"`
}

type episode struct {
	ID      string     `json:"id"`
	Season  int        `json:"season"`
	Episode int        `json:"episode"`
	Title   string     `json:"title"`
	Subs    []subtitle `json:"subs"`
}

type series struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Episodes []episode `json:"episodes"`
}

func jellyfinGet(cfg config, path string) ([]byte, error) {
	req, err := http.NewRequest("GET", cfg.jellyfinURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Emby-Token", cfg.apiKey)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("jellyfin %s -> %d", path, resp.StatusCode)
	}
	return body, nil
}

type mediaStream struct {
	Type       string `json:"Type"`
	Codec      string `json:"Codec"`
	Language   string `json:"Language"`
	Index      int    `json:"Index"`
	IsExternal bool   `json:"IsExternal"`
}

// externalSubs keeps only external, text-based (subrip) subtitle streams: that's exactly what
// Bazarr manages as sidecar .srt files. Embedded/image-based (e.g. PGS) tracks are skipped, since
// there's no equivalent plain-file download for those.
func externalSubs(streams []mediaStream) []subtitle {
	var subs []subtitle
	for _, s := range streams {
		if s.Type == "Subtitle" && s.IsExternal && s.Codec == "subrip" {
			lang := s.Language
			if lang == "" {
				lang = "?"
			}
			subs = append(subs, subtitle{Index: s.Index, Lang: lang})
		}
	}
	return subs
}

func fetchMovies(cfg config) ([]movie, error) {
	body, err := jellyfinGet(cfg, "/Items?IncludeItemTypes=Movie&Recursive=true&Fields=MediaStreams&SortBy=SortName")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Items []struct {
			Id           string        `json:"Id"`
			Name         string        `json:"Name"`
			MediaStreams []mediaStream `json:"MediaStreams"`
		} `json:"Items"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	var movies []movie
	for _, it := range resp.Items {
		if subs := externalSubs(it.MediaStreams); len(subs) > 0 {
			movies = append(movies, movie{ID: it.Id, Title: it.Name, Subs: subs})
		}
	}
	sort.Slice(movies, func(i, j int) bool { return movies[i].Title < movies[j].Title })
	return movies, nil
}

func fetchEpisodes(cfg config, seriesID string) ([]episode, error) {
	body, err := jellyfinGet(cfg, "/Shows/"+seriesID+"/Episodes?Fields=MediaStreams")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Items []struct {
			Id                string        `json:"Id"`
			Name              string        `json:"Name"`
			IndexNumber       int           `json:"IndexNumber"`
			ParentIndexNumber int           `json:"ParentIndexNumber"`
			MediaStreams      []mediaStream `json:"MediaStreams"`
		} `json:"Items"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	var episodes []episode
	for _, it := range resp.Items {
		if subs := externalSubs(it.MediaStreams); len(subs) > 0 {
			episodes = append(episodes, episode{
				ID: it.Id, Season: it.ParentIndexNumber, Episode: it.IndexNumber, Title: it.Name, Subs: subs,
			})
		}
	}
	sort.Slice(episodes, func(i, j int) bool {
		if episodes[i].Season != episodes[j].Season {
			return episodes[i].Season < episodes[j].Season
		}
		return episodes[i].Episode < episodes[j].Episode
	})
	return episodes, nil
}

func fetchSeries(cfg config) ([]series, error) {
	body, err := jellyfinGet(cfg, "/Items?IncludeItemTypes=Series&Recursive=true")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Items []struct {
			Id   string `json:"Id"`
			Name string `json:"Name"`
		} `json:"Items"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	// One fetch per series in parallel: a big show (e.g. hundreds of episodes) shouldn't make
	// every other series wait behind it just to find out most of them have no subtitles at all.
	results := make([]series, len(resp.Items))
	var wg sync.WaitGroup
	for i, it := range resp.Items {
		wg.Add(1)
		go func(i int, id, name string) {
			defer wg.Done()
			episodes, err := fetchEpisodes(cfg, id)
			if err != nil {
				log.Printf("fetchEpisodes %s: %v", id, err)
				return
			}
			if len(episodes) > 0 {
				results[i] = series{ID: id, Title: name, Episodes: episodes}
			}
		}(i, it.Id, it.Name)
	}
	wg.Wait()

	var out []series
	for _, s := range results {
		if s.ID != "" {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out, nil
}

type itemsResponse struct {
	Movies []movie  `json:"movies"`
	Series []series `json:"series"`
}

// itemsCache avoids re-walking the whole library (including every series' episodes) on every
// page load: the library changes on the order of hours (a new Bazarr grab, a new episode), not
// seconds, so a short TTL turns repeat loads near-instant without ever serving very stale data.
const itemsCacheTTL = 2 * time.Minute

var (
	itemsCacheMu   sync.Mutex
	itemsCacheData itemsResponse
	itemsCacheAt   time.Time
)

func getItems(cfg config) (itemsResponse, error) {
	itemsCacheMu.Lock()
	defer itemsCacheMu.Unlock()
	if time.Since(itemsCacheAt) < itemsCacheTTL {
		return itemsCacheData, nil
	}
	movies, err := fetchMovies(cfg)
	if err != nil {
		return itemsResponse{}, err
	}
	ser, err := fetchSeries(cfg)
	if err != nil {
		return itemsResponse{}, err
	}
	itemsCacheData = itemsResponse{Movies: movies, Series: ser}
	itemsCacheAt = time.Now()
	return itemsCacheData, nil
}

func apiHandler(cfg config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := getItems(cfg)
		if err != nil {
			log.Printf("getItems: %v", err)
			http.Error(w, "could not reach Jellyfin", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(items)
	}
}

func downloadHandler(cfg config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/download/")
		if id == "" {
			http.NotFound(w, r)
			return
		}
		index := r.URL.Query().Get("index")
		lang := r.URL.Query().Get("lang")
		name := r.URL.Query().Get("name")
		if name == "" {
			name = "subtitle"
		}
		body, err := jellyfinGet(cfg, fmt.Sprintf("/Videos/%s/%s/Subtitles/%s/Stream.srt", id, id, index))
		if err != nil {
			log.Printf("download %s: %v", id, err)
			http.Error(w, "could not fetch subtitle", http.StatusBadGateway)
			return
		}
		filename := fmt.Sprintf("%s (%s).srt", name, lang)
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(body)
	}
}

// zipEpisodes writes every given episode's external subtitle(s) into zw, under dir/ if dir is
// non-empty (used to nest a series' episodes inside their own folder in the everything-zip).
func zipEpisodes(cfg config, zw *zip.Writer, dir string, episodes []episode) {
	for _, ep := range episodes {
		for _, s := range ep.Subs {
			body, err := jellyfinGet(cfg, fmt.Sprintf("/Videos/%s/%s/Subtitles/%d/Stream.srt", ep.ID, ep.ID, s.Index))
			if err != nil {
				log.Printf("zip: episode %s sub %d: %v", ep.ID, s.Index, err)
				continue
			}
			fname := fmt.Sprintf("S%02dE%02d.%s.srt", ep.Season, ep.Episode, s.Lang)
			if dir != "" {
				fname = dir + "/" + fname
			}
			fw, err := zw.Create(fname)
			if err != nil {
				continue
			}
			fw.Write(body)
		}
	}
}

// downloadAllHandler zips every episode's external subtitle(s) for one series.
func downloadAllHandler(cfg config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/download-all/")
		if id == "" {
			http.NotFound(w, r)
			return
		}
		name := r.URL.Query().Get("name")
		if name == "" {
			name = "series"
		}
		episodes, err := fetchEpisodes(cfg, id)
		if err != nil {
			log.Printf("download-all %s: %v", id, err)
			http.Error(w, "could not reach Jellyfin", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name+".zip"))
		zw := zip.NewWriter(w)
		zipEpisodes(cfg, zw, "", episodes)
		zw.Close()
	}
}

// downloadEverythingHandler zips every movie's and every series' external subtitles in one go:
// movies at the zip root, each series' episodes nested under a folder named after the series.
func downloadEverythingHandler(cfg config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := getItems(cfg)
		if err != nil {
			log.Printf("download-everything: %v", err)
			http.Error(w, "could not reach Jellyfin", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="Subtitulos.zip"`)
		zw := zip.NewWriter(w)
		for _, m := range items.Movies {
			for _, s := range m.Subs {
				body, err := jellyfinGet(cfg, fmt.Sprintf("/Videos/%s/%s/Subtitles/%d/Stream.srt", m.ID, m.ID, s.Index))
				if err != nil {
					log.Printf("download-everything: movie %s sub %d: %v", m.ID, s.Index, err)
					continue
				}
				fw, err := zw.Create(fmt.Sprintf("%s.%s.srt", m.Title, s.Lang))
				if err != nil {
					continue
				}
				fw.Write(body)
			}
		}
		for _, s := range items.Series {
			zipEpisodes(cfg, zw, s.Title, s.Episodes)
		}
		zw.Close()
	}
}

type cachedImage struct {
	body        []byte
	contentType string
	at          time.Time
}

// Posters change essentially never (a re-download replacing the file is the only case), so an
// in-memory cache well past this service's own restart cadence is safe and turns every repeat
// page load's ~40 poster fetches from a live Jellyfin round-trip into a map lookup.
const imageCacheTTL = 24 * time.Hour

var (
	imageCacheMu sync.Mutex
	imageCache   = map[string]cachedImage{}
)

// imageHandler proxies an item's poster from Jellyfin, so the api_key never reaches the browser.
func imageHandler(cfg config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/image/")
		if id == "" {
			http.NotFound(w, r)
			return
		}

		imageCacheMu.Lock()
		cached, ok := imageCache[id]
		imageCacheMu.Unlock()
		if ok && time.Since(cached.at) < imageCacheTTL {
			w.Header().Set("Content-Type", cached.contentType)
			w.Header().Set("Cache-Control", "public, max-age=86400")
			w.Write(cached.body)
			return
		}

		req, err := http.NewRequest("GET", cfg.jellyfinURL+"/Items/"+id+"/Images/Primary?maxHeight=300&quality=85", nil)
		if err != nil {
			http.Error(w, "bad request", http.StatusInternalServerError)
			return
		}
		req.Header.Set("X-Emby-Token", cfg.apiKey)
		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Do(req)
		if err != nil || resp.StatusCode >= 300 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		ct := resp.Header.Get("Content-Type")

		imageCacheMu.Lock()
		imageCache[id] = cachedImage{body: body, contentType: ct, at: time.Now()}
		imageCacheMu.Unlock()

		if ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(body)
	}
}

func main() {
	cfg := loadConfig()
	log.Printf("subtitle-links started, jellyfin=%s", cfg.jellyfinURL)

	mux := http.NewServeMux()
	mux.HandleFunc("/", indexHandler)
	mux.HandleFunc("/icon.svg", iconHandler)
	mux.HandleFunc("/api/items", apiHandler(cfg))
	mux.HandleFunc("/image/", imageHandler(cfg))
	mux.HandleFunc("/download/", downloadHandler(cfg))
	mux.HandleFunc("/download-all/", downloadAllHandler(cfg))
	mux.HandleFunc("/download-all", downloadEverythingHandler(cfg))
	log.Fatal(http.ListenAndServe(":"+listenPort, mux))
}
