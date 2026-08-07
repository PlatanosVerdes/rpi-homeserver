// subtitle-links serves a small page listing every movie that has an external (Bazarr-downloaded)
// .srt subtitle, with a direct download link per language, so subtitles can be grabbed by hand onto
// a device (e.g. to load into VLC alongside an offline-downloaded video) without hand-building URLs.
package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
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
	Index int
	Lang  string
}

type movie struct {
	ID    string
	Title string
	Subs  []subtitle
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

// fetchMovies only surfaces external, text-based (subrip) subtitle streams: that's exactly what
// Bazarr manages as sidecar .srt files. Embedded/image-based (e.g. PGS) tracks are skipped, since
// there's no equivalent plain-file download for those.
func fetchMovies(cfg config) ([]movie, error) {
	body, err := jellyfinGet(cfg, "/Items?IncludeItemTypes=Movie&Recursive=true&Fields=MediaStreams&SortBy=SortName")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Items []struct {
			Id           string `json:"Id"`
			Name         string `json:"Name"`
			MediaStreams []struct {
				Type       string `json:"Type"`
				Codec      string `json:"Codec"`
				Language   string `json:"Language"`
				Index      int    `json:"Index"`
				IsExternal bool   `json:"IsExternal"`
			} `json:"MediaStreams"`
		} `json:"Items"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	var movies []movie
	for _, it := range resp.Items {
		var subs []subtitle
		for _, s := range it.MediaStreams {
			if s.Type == "Subtitle" && s.IsExternal && s.Codec == "subrip" {
				lang := s.Language
				if lang == "" {
					lang = "?"
				}
				subs = append(subs, subtitle{Index: s.Index, Lang: lang})
			}
		}
		if len(subs) > 0 {
			movies = append(movies, movie{ID: it.Id, Title: it.Name, Subs: subs})
		}
	}
	sort.Slice(movies, func(i, j int) bool { return movies[i].Title < movies[j].Title })
	return movies, nil
}

const pageTemplate = `<!doctype html>
<html lang="es">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Subtitulos</title>
<style>
  body { font-family: -apple-system, sans-serif; background:#111; color:#eee; margin:0; padding:1.5rem; }
  h1 { font-size:1.3rem; margin-bottom:1rem; }
  ul { list-style:none; padding:0; margin:0; }
  li { background:#1c1c1c; border-radius:10px; padding:0.9rem 1rem; margin-bottom:0.6rem;
       display:flex; justify-content:space-between; align-items:center; flex-wrap:wrap; gap:0.5rem; }
  .title { flex:1; min-width:55%; }
  .links { display:flex; gap:0.5rem; flex-wrap:wrap; }
  a.dl { background:#2d6cdf; color:#fff; text-decoration:none; padding:0.5rem 0.9rem;
         border-radius:8px; font-size:0.95rem; white-space:nowrap; }
  a.dl:active { background:#1e4fa8; }
</style>
</head>
<body>
<h1>Subtitulos descargables ({{len .}} pelis)</h1>
<ul>
{{range $m := .}}
  <li>
    <span class="title">{{$m.Title}}</span>
    <span class="links">
    {{range $m.Subs}}
      <a class="dl" href="/download/{{$m.ID}}?index={{.Index}}&amp;lang={{.Lang}}&amp;name={{$m.Title}}">{{.Lang}}</a>
    {{end}}
    </span>
  </li>
{{end}}
</ul>
</body>
</html>`

func indexHandler(cfg config) http.HandlerFunc {
	tmpl := template.Must(template.New("page").Parse(pageTemplate))
	return func(w http.ResponseWriter, r *http.Request) {
		movies, err := fetchMovies(cfg)
		if err != nil {
			log.Printf("fetchMovies: %v", err)
			http.Error(w, "could not reach Jellyfin", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.Execute(w, movies); err != nil {
			log.Printf("template: %v", err)
		}
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

func main() {
	cfg := loadConfig()
	log.Printf("subtitle-links started, jellyfin=%s", cfg.jellyfinURL)

	mux := http.NewServeMux()
	mux.HandleFunc("/", indexHandler(cfg))
	mux.HandleFunc("/download/", downloadHandler(cfg))
	log.Fatal(http.ListenAndServe(":"+listenPort, mux))
}
