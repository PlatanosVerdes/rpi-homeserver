// audio-convert lists the files a client had its audio re-encoded on, with the plan pi-metrics
// worked out for each, and converts one when asked. pi-metrics does the thinking (which track, what
// it costs, whether a file change can help at all); scripts/sync/audio-compat.py does the work, the
// same script the night run uses; this page only joins the two and shows the progress.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	listenAddr = ":8088"
	// audio-compat's MIN_FREE_BYTES: what must be left after a copy of the file.
	minFreeBytes = 40 << 30
	historyShown = 15
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type server struct {
	ledgerFile string
	stateDir   string
	stateFile  string
	lockFile   string
	history    string
	dataRoot   string
	python     string
	script     string
	retry      time.Duration

	mu      sync.Mutex
	queue   []job
	results map[string]outcome
	running *progress
	waiting map[string]any
	wake    chan struct{}
}

func newServer() *server {
	stateDir := env("AUDIO_CONVERT_STATE", "/state")
	return &server{
		ledgerFile: env("AUDIO_RECODES", "/ledger/audio-recodes.json"),
		stateDir:   stateDir,
		stateFile:  defaultStateFile(stateDir),
		lockFile:   filepath.Join(stateDir, "convert.lock"),
		history:    filepath.Join(stateDir, "history.jsonl"),
		dataRoot:   env("DATA_ROOT", "/mnt/data"),
		python:     env("PYTHON", "python3"),
		script:     env("AUDIO_COMPAT", "/scripts/audio-compat.py"),
		retry:      30 * time.Second,
		results:    map[string]outcome{},
		wake:       make(chan struct{}, 1),
	}
}

func main() {
	s := newServer()
	if err := os.MkdirAll(s.stateDir, 0o755); err != nil {
		log.Fatalf("audio-convert: %v", err)
	}
	s.load()

	http.HandleFunc("/", s.index)
	http.HandleFunc("/icon.svg", icon)
	http.HandleFunc("/api/state", s.state)
	http.HandleFunc("/api/play", s.play)
	http.HandleFunc("/api/cancel", s.cancel)

	go s.worker()

	log.Printf("audio-convert: %s, ledger %s, library %s", listenAddr, s.ledgerFile, s.dataRoot)
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

type item struct {
	ledgerFile
	Exists      bool     `json:"exists"`
	Size        int64    `json:"size"`
	Links       uint64   `json:"links"`
	EnoughSpace bool     `json:"enough_space"`
	ETA         float64  `json:"eta"`
	Status      string   `json:"status"` // ready, queued, converting, done, failed, missing, info
	Outcome     *outcome `json:"outcome,omitempty"`
}

func (s *server) items(files []ledgerFile, history []historyEntry, speed float64) []item {
	converted := map[string]historyEntry{}
	for _, entry := range history {
		if entry.Result == "done" {
			converted[libraryPath(entry.Path)] = entry
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]item, 0, len(files))
	for _, f := range files {
		it := item{ledgerFile: f, Status: "info"}
		if full, ok := insideLibrary(s.dataRoot, f.Library); ok {
			facts := inspect(full)
			it.Exists, it.Size, it.Links = facts.exists, facts.size, facts.links
			it.EnoughSpace = facts.exists && facts.free >= uint64(facts.size)+minFreeBytes
		}
		it.ETA = eta(f.Duration, 0, speed)
		if f.Actionable {
			it.Status = s.status(&it, converted)
		}
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Actionable != out[j].Actionable {
			return out[i].Actionable
		}
		return out[i].Seen > out[j].Seen
	})
	return out
}

// status is called with s.mu held.
func (s *server) status(it *item, converted map[string]historyEntry) string {
	if s.running != nil && s.running.Library == it.Library {
		return "converting"
	}
	if slices.ContainsFunc(s.queue, func(q job) bool { return q.Library == it.Library }) {
		return "queued"
	}
	if done, ok := converted[it.Library]; ok && done.Time >= it.Seen {
		it.Outcome = &outcome{Time: done.Time, Result: "done", Mode: done.Mode}
		return "done"
	}
	if last, ok := s.results[it.Library]; ok && last.Time >= it.Seen {
		it.Outcome = &last
		if last.Result == "done" {
			return "done"
		}
		return "failed"
	}
	if !it.Exists {
		return "missing"
	}
	return "ready"
}

func (s *server) state(w http.ResponseWriter, r *http.Request) {
	files, ledgerErr := readLedger(s.ledgerFile)
	history, _ := readHistory(s.history)
	speed := averageSpeed(history)
	listed := s.items(files, history, speed)

	recent := make([]historyEntry, 0, historyShown)
	for i := len(history) - 1; i >= 0 && len(recent) < historyShown; i-- {
		recent = append(recent, history[i])
	}

	s.mu.Lock()
	running := s.running
	var runningView map[string]any
	if running != nil {
		current := *running
		if current.Speed <= 0 {
			current.Speed = speed
		}
		runningView = map[string]any{
			"job": current, "eta": eta(current.Duration, current.Seconds, current.Speed),
		}
	}
	queue := slices.Clone(s.queue)
	waiting := s.waiting
	s.mu.Unlock()

	// The lock is ours while our own conversion runs, so it only says something about the night
	// run when nothing here is converting.
	holder := waiting
	if running == nil && holder == nil {
		holder, _ = lockHolder(s.lockFile)
	}

	payload := map[string]any{
		"items":   listed,
		"queue":   queue,
		"running": runningView,
		"holder":  holder,
		"history": recent,
		"speed":   speed,
		"free":    freeBytes(s.dataRoot),
		"minFree": minFreeBytes,
	}
	if ledgerErr != nil {
		payload["error"] = "pi-metrics' ledger is unreadable: " + ledgerErr.Error()
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *server) play(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	library := strings.TrimSpace(r.FormValue("library_path"))
	allowHardlinked := r.FormValue("allow_hardlinked") != ""

	// Only what pi-metrics listed as fixable can be queued, so this is no way to remux any path.
	files, err := readLedger(s.ledgerFile)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	i := slices.IndexFunc(files, func(f ledgerFile) bool { return f.Library == library })
	if i < 0 || !files[i].Actionable {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "ese fichero no está en la lista de convertibles"})
		return
	}
	full, ok := insideLibrary(s.dataRoot, library)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "ruta fuera de la biblioteca"})
		return
	}
	facts := inspect(full)
	switch {
	case !facts.exists:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "el fichero ya no está; quizá se reemplazó"})
		return
	case facts.links > 1 && !allowHardlinked:
		writeJSON(w, http.StatusConflict, map[string]any{"error": "sigue sembrando", "confirm": true})
		return
	case facts.free < uint64(facts.size)+minFreeBytes:
		writeJSON(w, http.StatusConflict, map[string]any{"error": "no hay sitio: tras copiar el fichero deben quedar 40 GiB libres"})
		return
	}

	title := files[i].Title
	if title == "" {
		title = filepath.Base(full)
	}
	if !s.enqueue(job{Library: library, Title: title, AllowHardlinked: allowHardlinked, Added: time.Now().Unix()}) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "ya está en la cola"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"queued": true})
}

func (s *server) cancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !s.dequeue(strings.TrimSpace(r.FormValue("library_path"))) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no estaba en la cola (o ya ha empezado)"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
