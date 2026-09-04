// torrent-drop hands qBittorrent a .torrent file, a magnet or a bare infohash from one page, so a
// download by hand does not mean opening the WebUI and logging into it, and asks cross-seed about
// every torrent the moment it completes: the daemon's own sweep is once a day, so this is the
// difference between free ratio now and free ratio tomorrow.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	listenAddr = ":8087"

	// Its own category and tag, because a manual download is judged by its own share_limits group
	// in config/qbit-manage/config.yml: it seeds the tracker's term and is then stopped, never
	// deleted. Renaming either here means renaming it there.
	manualCategory = "manual"
	manualSavePath = "/data/downloads"
	manualTag      = "manual"
	// The manual group excludes it, as every other group does, so it means "keep seeding, do not
	// stop this one".
	keepTag = "keep"
	// Written once cross-seed has been asked about a torrent. The marker lives in qBittorrent so a
	// restart here does not re-ask about all 40-odd of them.
	searchedTag = "xseed"
	// cross-seed's own injections: asking it about its own output finds nothing by design.
	crossSeedCategory = "cross-seed-link"

	pollInterval   = 30 * time.Second
	maxUploadBytes = 20 << 20
)

var (
	qbitURL      = env("QBIT_URL", "http://qbittorrent:8080")
	crossSeedURL = env("CROSS_SEED_URL", "http://cross-seed:2468")

	client = &http.Client{Timeout: 30 * time.Second}
	// A search walks the four private indexers with cross-seed's own 30s delay between them and
	// the webhook only answers when it is done, so this one cannot share the timeout above.
	searchClient = &http.Client{Timeout: 5 * time.Minute}

	infoHashRe = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type torrent struct {
	Hash     string  `json:"hash"`
	Name     string  `json:"name"`
	Category string  `json:"category"`
	Tags     string  `json:"tags"`
	State    string  `json:"state"`
	Progress float64 `json:"progress"`
	Size     int64   `json:"size"`
	DLSpeed  int64   `json:"dlspeed"`
	Ratio    float64 `json:"ratio"`
	AddedOn  int64   `json:"added_on"`
}

func (t torrent) hasTag(tag string) bool {
	for _, have := range strings.Split(t.Tags, ",") {
		if strings.TrimSpace(have) == tag {
			return true
		}
	}
	return false
}

type server struct {
	mu        sync.Mutex
	freeSpace int64
	sweepErr  string
}

func main() {
	s := &server{}
	if err := ensureCategory(); err != nil {
		log.Printf("torrent-drop: could not create the %s category: %v", manualCategory, err)
	}

	http.HandleFunc("/", s.index)
	http.HandleFunc("/icon.svg", icon)
	http.HandleFunc("/api/list", s.list)
	http.HandleFunc("/api/add", s.add)
	http.HandleFunc("/api/cross-seed", s.crossSeedNow)

	go s.pollLoop()

	log.Printf("torrent-drop: %s, qBittorrent at %s, cross-seed at %s", listenAddr, qbitURL, crossSeedURL)
	log.Fatal(http.ListenAndServe(listenAddr, nil))
}

func (s *server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	render(w)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(payload)
}

func (s *server) list(w http.ResponseWriter, r *http.Request) {
	torrents, err := manualTorrents()
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	s.mu.Lock()
	free, sweepErr := s.freeSpace, s.sweepErr
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"torrents":   torrents,
		"freeSpace":  free,
		"sweepError": sweepErr,
	})
}

func manualTorrents() ([]torrent, error) {
	body, err := qbitGet("torrents/info", url.Values{"category": {manualCategory}})
	if err != nil {
		return nil, err
	}
	var torrents []torrent
	if err := json.Unmarshal([]byte(body), &torrents); err != nil {
		return nil, err
	}
	sort.Slice(torrents, func(i, j int) bool { return torrents[i].AddedOn > torrents[j].AddedOn })
	return torrents, nil
}

// add takes whatever the page collected: dropped .torrent files, and text that can be a magnet, an
// http link to a .torrent or a bare infohash. Both travel in the one request qBittorrent accepts.
func (s *server) add(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "no pude leer lo que enviaste: " + err.Error()})
		return
	}

	links, bad := parseLinks(r.FormValue("links"))
	var files []*multipart.FileHeader
	if r.MultipartForm != nil {
		files = r.MultipartForm.File["torrents"]
	}
	if len(links) == 0 && len(files) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "nada que añadir", "rejected": bad})
		return
	}

	tags := manualTag
	if r.FormValue("keep") != "" {
		tags += "," + keepTag
	}

	var payload bytes.Buffer
	form := multipart.NewWriter(&payload)
	fields := map[string]string{
		"category": manualCategory,
		"savepath": manualSavePath,
		"tags":     tags,
		"autoTMM":  "false",
		"paused":   "false",
	}
	if len(links) > 0 {
		fields["urls"] = strings.Join(links, "\n")
	}
	for name, value := range fields {
		form.WriteField(name, value)
	}
	for _, header := range files {
		if err := attach(form, header); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	form.Close()

	body, err := qbitPost("torrents/add", form.FormDataContentType(), payload.Bytes())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	// A 200 carrying "Fails." is qBittorrent rejecting every item, which is what a corrupt file or
	// an unreachable link looks like.
	if strings.Contains(body, "Fails") {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "qBittorrent no aceptó nada de esto"})
		return
	}

	log.Printf("torrent-drop: added %d file(s) and %d link(s), tags %q", len(files), len(links), tags)
	writeJSON(w, http.StatusOK, map[string]any{"added": len(files) + len(links), "rejected": bad})
}

func attach(form *multipart.Writer, header *multipart.FileHeader) error {
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".torrent") {
		return fmt.Errorf("%s no es un .torrent", header.Filename)
	}
	file, err := header.Open()
	if err != nil {
		return err
	}
	defer file.Close()
	part, err := form.CreateFormFile("torrents", header.Filename)
	if err != nil {
		return err
	}
	_, err = io.Copy(part, file)
	return err
}

// parseLinks keeps the lines qBittorrent can act on and returns the rest, so a typo comes back to
// the page instead of being added as a torrent that never resolves.
func parseLinks(text string) (links, rejected []string) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case strings.HasPrefix(line, "magnet:"),
			strings.HasPrefix(line, "http://"),
			strings.HasPrefix(line, "https://"):
			links = append(links, line)
		case infoHashRe.MatchString(line):
			links = append(links, "magnet:?xt=urn:btih:"+strings.ToLower(line))
		default:
			rejected = append(rejected, line)
		}
	}
	return links, rejected
}

func (s *server) crossSeedNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	hash := strings.ToLower(strings.TrimSpace(r.FormValue("hash")))
	if !infoHashRe.MatchString(hash) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "eso no es un infohash"})
		return
	}
	if err := askCrossSeed(hash); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	if err := markSearched([]string{hash}); err != nil {
		log.Printf("torrent-drop: searched %s but could not tag it: %v", hash, err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// pollLoop asks cross-seed about torrents as they complete. The first pass only marks what is
// already complete: at startup that is every torrent on the disk, and searching all of them at
// once would be a burst of hundreds of queries at four private trackers for releases the daemon's
// own searchCadence has covered for months. Anything that finishes from now on gets asked about
// within pollInterval, and a torrent that completed while this was down waits for that daily sweep.
func (s *server) pollLoop() {
	baseline := true
	for {
		if err := s.sweep(baseline); err != nil {
			log.Printf("torrent-drop: sweep failed: %v", err)
			s.mu.Lock()
			s.sweepErr = err.Error()
			s.mu.Unlock()
		}
		baseline = false
		time.Sleep(pollInterval)
	}
}

func (s *server) sweep(baseline bool) error {
	body, err := qbitGet("torrents/info", nil)
	if err != nil {
		return err
	}
	var torrents []torrent
	if err := json.Unmarshal([]byte(body), &torrents); err != nil {
		return err
	}

	var search, mark []string
	for _, t := range torrents {
		if t.Progress < 1 || t.Category == crossSeedCategory || t.hasTag(searchedTag) {
			continue
		}
		// A manual download is searched even on the baseline pass: there are never many, so the
		// burst does not apply, and marking one without searching it would have the page claim
		// something it did not do.
		if baseline && t.Category != manualCategory {
			mark = append(mark, t.Hash)
			continue
		}
		search = append(search, t.Hash)
	}

	s.mu.Lock()
	s.sweepErr = ""
	s.mu.Unlock()

	if free, err := freeSpace(); err == nil {
		s.mu.Lock()
		s.freeSpace = free
		s.mu.Unlock()
	}

	if len(mark) > 0 {
		log.Printf("torrent-drop: %d torrents already complete at startup, marked without searching", len(mark))
		if err := markSearched(mark); err != nil {
			return err
		}
	}

	// One at a time and tagged as each one lands: a failed search must be retried on the next
	// sweep, and a batch tag would claim the ones that never ran.
	for _, hash := range search {
		if err := askCrossSeed(hash); err != nil {
			return fmt.Errorf("cross-seed refused %s: %w", hash, err)
		}
		if err := markSearched([]string{hash}); err != nil {
			return err
		}
		log.Printf("torrent-drop: cross-seed searched %s", hash)
	}
	return nil
}

func askCrossSeed(hash string) error {
	form := url.Values{
		"infoHash": {hash},
		// This is a deliberate ask about one torrent, so the daemon's own filters must not silence
		// it: excludeOlder drops anything released over 180 days ago and excludeRecentSearch
		// anything looked at in the last 45.
		"ignoreExcludeOlder":        {"true"},
		"ignoreExcludeRecentSearch": {"true"},
	}
	resp, err := searchClient.Post(crossSeedURL+"/api/webhook",
		"application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook answered %s", resp.Status)
	}
	return nil
}

func markSearched(hashes []string) error {
	_, err := qbitForm("torrents/addTags", url.Values{
		"hashes": {strings.Join(hashes, "|")},
		"tags":   {searchedTag},
	})
	return err
}

func ensureCategory() error {
	_, err := qbitForm("torrents/createCategory", url.Values{
		"category": {manualCategory},
		"savePath": {manualSavePath},
	})
	// 409 is "it already exists", which is the normal answer on every restart.
	if err != nil && strings.Contains(err.Error(), "409") {
		return nil
	}
	return err
}

func freeSpace() (int64, error) {
	body, err := qbitGet("sync/maindata", url.Values{"rid": {"0"}})
	if err != nil {
		return 0, err
	}
	var data struct {
		ServerState struct {
			FreeSpace int64 `json:"free_space_on_disk"`
		} `json:"server_state"`
	}
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		return 0, err
	}
	return data.ServerState.FreeSpace, nil
}
