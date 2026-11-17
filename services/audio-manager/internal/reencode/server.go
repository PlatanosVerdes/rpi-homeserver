// Package reencode is the Re-encode view: it lists the files a client had its audio re-encoded on,
// with the plan pi-metrics worked out for each, and converts one when asked. pi-metrics does the
// thinking (which track, what it costs, whether a file change can help at all),
// scripts/sync/audio-compat.py does the work, the same script the night run uses.
package reencode

import (
	"net/http"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"audio-manager/internal/httpx"
	"audio-manager/internal/libraries"
)

// audio-compat's MIN_FREE_BYTES: what must be left after a copy of the file.
const minFreeBytes = 40 << 30

type Config struct {
	LedgerFile string
	StateDir   string
	DataRoot   string
	Python     string
	Script     string
	ScanEvery  time.Duration
}

type Server struct {
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

	scanEvery time.Duration
	rescan    chan struct{}
	library   libraryScan
}

// New reads the queue and the last library scan the previous run left in cfg.StateDir.
func New(cfg Config) *Server {
	s := &Server{
		ledgerFile: cfg.LedgerFile,
		stateDir:   cfg.StateDir,
		stateFile:  filepath.Join(cfg.StateDir, "audio-manager.json"),
		lockFile:   filepath.Join(cfg.StateDir, "convert.lock"),
		history:    filepath.Join(cfg.StateDir, "history.jsonl"),
		dataRoot:   cfg.DataRoot,
		python:     cfg.Python,
		script:     cfg.Script,
		retry:      30 * time.Second,
		results:    map[string]outcome{},
		wake:       make(chan struct{}, 1),
		scanEvery:  cfg.ScanEvery,
		rescan:     make(chan struct{}, 1),
	}
	s.load()
	s.loadScan()
	return s
}

// Start runs the conversion queue and the library scan in the background.
func (s *Server) Start() {
	go s.worker()
	go s.scanner()
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

func (s *Server) items(files []ledgerFile, history []historyEntry, speed float64) []item {
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
func (s *Server) status(it *item, converted map[string]historyEntry) string {
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

// State is Re-encode's part of /api/state.
func (s *Server) State() map[string]any {
	files, ledgerErr := readLedger(s.ledgerFile)
	history, _ := readHistory(s.history)
	speed := averageSpeed(history)
	listed := s.items(files, history, speed)
	scanned, libraryOnly := s.libraryFiles(files)
	library := map[string]any{"time": scanned.Time, "items": s.items(libraryOnly, history, speed),
		"targets": scanned.Targets, "default_target": scanned.DefaultTarget}
	if scanned.Error != "" {
		library["error"] = scanned.Error
	}

	recent := make([]historyEntry, 0, httpx.HistoryShown)
	for i := len(history) - 1; i >= 0 && len(recent) < httpx.HistoryShown; i-- {
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
		"library": library,
		"queue":   queue,
		"running": runningView,
		"holder":  holder,
		"history": recent,
		"speed":   speed,
		"free":    freeBytes(filepath.Join(s.dataRoot, libraries.Folders[0])),
		"minFree": minFreeBytes,
	}
	if ledgerErr != nil {
		payload["error"] = "pi-metrics' ledger is unreadable: " + ledgerErr.Error()
	}
	return payload
}

func (s *Server) ServePlay(w http.ResponseWriter, r *http.Request) {
	if !httpx.PostOnly(w, r) {
		return
	}
	library := strings.TrimSpace(r.FormValue("library_path"))
	allowHardlinked := r.FormValue("allow_hardlinked") != ""
	chosen := strings.TrimSpace(r.FormValue("target"))
	if chosen != "" && !s.validTarget(chosen) {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "unknown target codec " + chosen})
		return
	}

	// Only what pi-metrics listed as fixable, or the library scan found, can be queued, so this is
	// no way to remux any path.
	files, err := readLedger(s.ledgerFile)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	_, scanned := s.libraryFiles(files)
	files = append(files, scanned...)
	i := slices.IndexFunc(files, func(f ledgerFile) bool { return f.Library == library })
	if i < 0 || !files[i].Actionable {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "that file is not listed as fixable"})
		return
	}
	full, ok := insideLibrary(s.dataRoot, library)
	if !ok {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "not a path inside the library"})
		return
	}
	facts := inspect(full)
	switch {
	case !facts.exists:
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"error": "the file is gone; it may have been replaced"})
		return
	case facts.links > 1 && !allowHardlinked:
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{"error": "still seeding (hardlinked); confirm to convert it anyway", "confirm": true})
		return
	case facts.free < uint64(facts.size)+minFreeBytes:
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{"error": "not enough free space: 40 GiB must be left after a copy of the file"})
		return
	}

	title := files[i].Title
	if title == "" {
		title = filepath.Base(full)
	}
	queued := job{Library: library, Title: title, AllowHardlinked: allowHardlinked, Target: chosen, Added: time.Now().Unix()}
	if p := files[i].Plan; p != nil {
		sources := p.Sources
		if len(sources) == 0 {
			sources = []planTrack{p.Source}
		}
		for _, source := range sources {
			queued.Tracks = append(queued.Tracks, source.Position)
		}
	}
	if !s.enqueue(queued) {
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{"error": "already queued"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"queued": true})
}

func (s *Server) ServeCancel(w http.ResponseWriter, r *http.Request) {
	if !httpx.PostOnly(w, r) {
		return
	}
	if !s.dequeue(strings.TrimSpace(r.FormValue("library_path"))) {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"error": "not in the queue (or already started)"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}
