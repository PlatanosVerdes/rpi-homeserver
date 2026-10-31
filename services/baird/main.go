package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Decides which channels there are: the upstream lists minus what was switched off, plus web pages
// that link acestream ids and channels picked out of the engine's catalogue, written as one M3U. It
// never plays anything, and knows nothing of who reads the list.
func main() {
	listenAddr := getenv("LISTEN_ADDR", ":8089")
	dir := getenv("CONFIG_DIR", "/config")
	refresh, listsEvery := duration("REFRESH", "30m"), duration("LISTS_EVERY", "2m")
	maxKept, err := strconv.Atoi(getenv("MAX_KEPT", "3"))
	if err != nil {
		log.Fatalf("baird: MAX_KEPT: %v", err)
	}

	a := &app{
		store:  openStore(dir),
		engine: &engine{base: strings.TrimRight(getenv("ENGINE_URL", "http://aceserve:6878"), "/"), client: newClient(2 * time.Minute)},
		web:    newClient(30 * time.Second),
		kick:   make(chan pass, 1),
		lists:  getenv("UPSTREAM_FILE", "/upstream/upstream.json"),
		player: os.Getenv("PLAYER_URL"),
		keep:   keeping{For: duration("KEEP_DROPPED", "72h"), Max: maxKept},
	}
	go a.loop(refresh, listsEvery)

	http.HandleFunc("/", a.index)
	http.HandleFunc("/baird.m3u", a.list)
	http.HandleFunc("/api/state", a.state)
	http.HandleFunc("/api/pick", a.choose(a.store.setPicked))
	http.HandleFunc("/api/categories", a.choose(a.store.setCategories))
	http.HandleFunc("/api/countries", a.choose(a.store.setCountries))
	http.HandleFunc("/api/listgroups", a.choose(a.store.setListGroups))
	http.HandleFunc("/api/listchannels", a.choose(a.store.setListChannels))
	http.HandleFunc("/api/pages", a.pages)
	http.HandleFunc("/icon.svg", icon)
	http.HandleFunc("/apple-touch-icon.png", appleTouchIcon)
	http.HandleFunc("/logo", a.logo)

	log.Printf("baird: %s, engine %s, files in %s, lists every %s, the rest every %s", listenAddr, a.engine.base, dir, listsEvery, refresh)
	log.Fatal(http.ListenAndServe(listenAddr, nil))
}

type app struct {
	store  *store
	engine *engine
	web    *http.Client
	kick   chan pass
	lists  string // the upstream lists file
	player string // optional: who plays the list, asked which sources failed
	keep   keeping
}

// pass is how much an update fetches again: everything, the upstream lists, or nothing.
type pass int

const (
	rebuild pass = iota
	upstreamOnly
	everything
)

// The upstream lists rotate ids several times an hour, so they are read far more often than the
// catalogue and the pages.
func (a *app) loop(every, listsEvery time.Duration) {
	a.update(everything)
	slow, fast := time.NewTicker(every), time.NewTicker(listsEvery)
	for {
		select {
		case <-slow.C:
			a.update(everything)
		case <-fast.C:
			a.update(upstreamOnly)
		case p := <-a.kick:
			a.update(p)
		}
	}
}

func (a *app) poke(p pass) {
	select {
	case a.kick <- p:
	default:
	}
}

func (a *app) update(p pass) {
	if p >= upstreamOnly {
		read, err := readUpstream(a.web, a.lists, a.store.path("lists"))
		if err != nil {
			log.Printf("baird: upstream: %v", err)
		}
		a.store.setUpstream(read, err, outcomes(a.web, a.player), a.keep)
	}
	if p == everything {
		a.store.mu.Lock()
		urls := make([]string, 0, len(a.store.choices.Pages))
		for _, p := range a.store.choices.Pages {
			urls = append(urls, p.URL)
		}
		a.store.mu.Unlock()
		for _, u := range urls {
			ids, err := scrape(a.web, u)
			if err != nil {
				log.Printf("baird: %s: %v", u, err)
			}
			a.store.pageResult(u, ids, err)
		}
		c, err := a.engine.catalogue()
		if err != nil {
			log.Printf("baird: catalogue: %v, keeping the last one", err)
		}
		a.store.setCatalogue(c, err)
	}

	found := map[string]string{}
	for _, h := range a.store.missingIDs() {
		id, err := a.engine.contentID(h)
		if err != nil {
			log.Printf("baird: %s: %v", h, err)
			continue
		}
		found[h] = id
	}
	a.store.learnIDs(found)

	if err := a.store.render(); err != nil {
		log.Printf("baird: writing the list: %v", err)
	}
	a.store.mu.Lock()
	log.Printf("baird: list has %d channels, %d sources", a.store.channels, a.store.sources)
	a.store.mu.Unlock()
}

func (a *app) list(w http.ResponseWriter, r *http.Request) {
	m3u := a.store.playlist()
	if m3u == "" {
		http.Error(w, "not built yet", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "audio/x-mpegurl")
	w.Write([]byte(m3u))
}

func (a *app) state(w http.ResponseWriter, r *http.Request) {
	s := a.store
	s.mu.Lock()
	type row struct {
		Name       string   `json:"name"`
		Icon       string   `json:"icon,omitempty"`
		Categories []string `json:"categories,omitempty"`
		Countries  []string `json:"countries,omitempty"`
		Languages  []string `json:"languages,omitempty"`
		Sources    int      `json:"sources"`
		Available  int      `json:"available"`
		Bitrate    int      `json:"bitrate"`
		On         bool     `json:"on"`
		ByRule     bool     `json:"byRule,omitempty"`
	}
	rows := make([]row, 0, len(s.catalogue))
	for _, e := range s.catalogue {
		r := row{Name: e.Name, Icon: e.Icon, Categories: e.Categories, Countries: e.Countries, Languages: e.Languages, Sources: len(e.Items),
			On: s.included(e), ByRule: s.ruled(e)}
		for _, it := range e.Items {
			if it.Availability > 0 {
				r.Available++
			}
			r.Bitrate = max(r.Bitrate, it.Bitrate)
		}
		rows = append(rows, r)
	}
	type listRow struct {
		Name    string `json:"name"`
		Group   string `json:"group,omitempty"`
		Logo    string `json:"logo,omitempty"`
		Sources int    `json:"sources"`
		Kept    int    `json:"kept"` // sources the upstream dropped and are still kept
		Shown   bool   `json:"shown"`
	}
	listRows := make([]listRow, 0, len(s.listed))
	for _, c := range s.listed {
		r := listRow{Name: c.Name, Group: c.Group, Logo: c.Logo, Sources: len(c.Sources), Shown: s.shown(c)}
		for _, src := range c.Sources {
			if src.Seen.Before(s.upstreamAt) {
				r.Kept++
			}
		}
		listRows = append(listRows, r)
	}
	body := map[string]any{
		"lists":        listRows,
		"listsErr":     s.listsErr,
		"hiddenGroups": append([]string(nil), s.choices.HiddenGroups...),
		"newGroups":    append([]string(nil), s.choices.NewGroups...),
		"pages":        append([]page(nil), s.choices.Pages...),
		"categories":   append([]string(nil), s.choices.Categories...),
		"countries":    append([]string(nil), s.choices.Countries...),
		"catalogue":    rows,
		"catalogAt":    s.catalogAt,
		"catalogErr":   s.catalogErr,
		"builtAt":      s.builtAt,
		"channels":     s.channels,
		"sources":      s.sources,
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(body)
}

// choose serves the two tick lists, channels and whole categories, which share one body.
func (a *app) choose(set func(names []string, picked bool)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Names  []string `json:"names"`
			Picked bool     `json:"picked"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Names) == 0 {
			http.Error(w, "expected {names, picked}", http.StatusBadRequest)
			return
		}
		set(body.Names, body.Picked)
		a.poke(rebuild)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *app) pages(w http.ResponseWriter, r *http.Request) {
	var body page
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "expected a JSON body", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodPost:
		u, err := url.Parse(body.URL)
		body.Name = strings.TrimSpace(body.Name)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || body.Name == "" {
			http.Error(w, "expected an http(s) url and a name", http.StatusBadRequest)
			return
		}
		ids, err := scrape(a.web, body.URL)
		if err != nil {
			http.Error(w, "no acestream links read from it: "+err.Error(), http.StatusUnprocessableEntity)
			return
		}
		body.IDs, body.Checked, body.Error = ids, time.Now(), ""
		if err := a.store.addPage(body); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
	case http.MethodDelete:
		a.store.removePage(body.URL)
	default:
		http.Error(w, "POST or DELETE", http.StatusMethodNotAllowed)
		return
	}
	a.poke(rebuild)
	w.WriteHeader(http.StatusNoContent)
}

func duration(key, def string) time.Duration {
	d, err := time.ParseDuration(getenv(key, def))
	if err != nil {
		log.Fatalf("baird: %s: %v", key, err)
	}
	return d
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
