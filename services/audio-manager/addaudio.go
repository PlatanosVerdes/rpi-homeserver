package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Adding a dub is scripts/sync/dub-fetch.py's job, run by cron on the host. The page and the script
// share nothing but the files in add-audio/: the script lists the films and its jobs, the page drops
// one request file per film and language, and the script consumes it.

type dubFilm struct {
	ID      int64    `json:"id"`
	Title   string   `json:"title"`
	Year    int      `json:"year"`
	IMDb    string   `json:"imdb"`
	Audio   string   `json:"audio"`
	Missing []string `json:"missing"`
}

type dubFilms struct {
	Time      float64           `json:"time"`
	Languages map[string]string `json:"languages"`
	Films     []dubFilm         `json:"films"`
}

// dubJob holds only what the page may show: dub-fetch's state also carries indexer URLs.
type dubJob struct {
	MovieID   int64       `json:"movie_id"`
	Lang      string      `json:"lang"`
	Title     string      `json:"title"`
	Requested float64     `json:"requested"`
	Status    string      `json:"status"`
	Reason    string      `json:"reason"`
	Updated   float64     `json:"updated"`
	Downloads int         `json:"downloads"`
	Tried     int         `json:"tried"`
	Current   *dubRelease `json:"current"`
}

type dubRelease struct {
	Title   string  `json:"title"`
	Size    int64   `json:"size"`
	Indexer string  `json:"indexer"`
	Private bool    `json:"private"`
	Stage   string  `json:"stage"`
	Since   float64 `json:"since"`
	Dub     string  `json:"dub"`
}

func (j dubJob) active() bool {
	return j.Status != "done" && j.Status != "failed" && j.Status != "cancelled"
}

type dubRequest struct {
	MovieID   int64  `json:"movie_id"`
	Lang      string `json:"lang"`
	Requested int64  `json:"requested"`
	Cancel    bool   `json:"cancel,omitempty"`
}

func dubKey(movieID int64, lang string) string { return fmt.Sprintf("%d-%s", movieID, lang) }

func (s *server) addAudioDir() string   { return filepath.Join(s.stateDir, "add-audio") }
func (s *server) dubRequestDir() string { return filepath.Join(s.addAudioDir(), "requests") }

// readJSON leaves out untouched when the file does not exist.
func readJSON(file string, out any) error {
	raw, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(file), err)
	}
	return nil
}

func (s *server) readDubFilms() (dubFilms, error) {
	var films dubFilms
	err := readJSON(filepath.Join(s.addAudioDir(), "films.json"), &films)
	return films, err
}

func (s *server) readDubJobs() (map[string]dubJob, int64, error) {
	var state struct {
		ChargedBytes int64 `json:"charged_bytes"`
		Jobs         map[string]struct {
			dubJob
			Tried []json.RawMessage `json:"tried"`
		} `json:"jobs"`
	}
	if err := readJSON(filepath.Join(s.addAudioDir(), "state.json"), &state); err != nil {
		return nil, 0, err
	}
	jobs := make(map[string]dubJob, len(state.Jobs))
	for key, raw := range state.Jobs {
		job := raw.dubJob
		job.Tried = len(raw.Tried)
		jobs[key] = job
	}
	return jobs, state.ChargedBytes, nil
}

func (s *server) readDubRequests() ([]dubRequest, error) {
	entries, err := os.ReadDir(s.dubRequestDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []dubRequest
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var request dubRequest
		if err := readJSON(filepath.Join(s.dubRequestDir(), entry.Name()), &request); err == nil && request.Lang != "" {
			out = append(out, request)
		}
	}
	return out, nil
}

func (s *server) dubRequestFile(movieID int64, lang string) string {
	return filepath.Join(s.dubRequestDir(), dubKey(movieID, lang)+".json")
}

// addAudioState is the add_audio object of /api/state.
func (s *server) addAudioState() map[string]any {
	films, filmsErr := s.readDubFilms()
	jobs, charged, jobsErr := s.readDubJobs()
	requests, requestsErr := s.readDubRequests()

	list := make([]dubJob, 0, len(jobs))
	for _, job := range jobs {
		list = append(list, job)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].active() != list[j].active() {
			return list[i].active()
		}
		return list[i].Updated > list[j].Updated
	})
	finished := 0
	shown := list[:0]
	for _, job := range list {
		if !job.active() {
			if finished++; finished > historyShown {
				continue
			}
		}
		shown = append(shown, job)
	}

	if films.Films == nil {
		films.Films = []dubFilm{}
	}
	if films.Languages == nil {
		films.Languages = map[string]string{}
	}
	if requests == nil {
		requests = []dubRequest{}
	}
	out := map[string]any{
		"time":          films.Time,
		"languages":     films.Languages,
		"films":         films.Films,
		"jobs":          shown,
		"requests":      requests,
		"charged_bytes": charged,
	}
	if err := errors.Join(filmsErr, jobsErr, requestsErr); err != nil {
		out["error"] = "dub-fetch's files are unreadable: " + err.Error()
	}
	return out
}

// dubTarget reads and checks the movie_id and lang of a request against what dub-fetch listed.
func (s *server) dubTarget(r *http.Request) (int64, string, int, error) {
	lang := strings.TrimSpace(r.FormValue("lang"))
	movieID, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("movie_id")), 10, 64)
	if err != nil {
		return 0, "", http.StatusBadRequest, errors.New("movie_id must be a number")
	}
	films, err := s.readDubFilms()
	if err != nil {
		return 0, "", http.StatusInternalServerError, err
	}
	if _, ok := films.Languages[lang]; !ok {
		return 0, "", http.StatusBadRequest, fmt.Errorf("unknown language %q", lang)
	}
	for _, film := range films.Films {
		if film.ID == movieID {
			for _, missing := range film.Missing {
				if missing == lang {
					return movieID, lang, 0, nil
				}
			}
		}
	}
	return 0, "", http.StatusBadRequest, errors.New("that film is not listed as missing that language")
}

func (s *server) activeDubJob(movieID int64, lang string) (bool, error) {
	jobs, _, err := s.readDubJobs()
	if err != nil {
		return false, err
	}
	job, ok := jobs[dubKey(movieID, lang)]
	return ok && job.active(), nil
}

func (s *server) writeDubRequest(request dubRequest) error {
	if err := os.MkdirAll(s.dubRequestDir(), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	return writeAtomic(s.dubRequestFile(request.MovieID, request.Lang), raw)
}

func (s *server) addAudio(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	movieID, lang, code, err := s.dubTarget(r)
	if err != nil {
		writeJSON(w, code, map[string]any{"error": err.Error()})
		return
	}

	s.dubMu.Lock()
	defer s.dubMu.Unlock()
	active, err := s.activeDubJob(movieID, lang)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if active {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "already being added"})
		return
	}
	if _, err := os.Stat(s.dubRequestFile(movieID, lang)); err == nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "already requested"})
		return
	}
	if err := s.writeDubRequest(dubRequest{MovieID: movieID, Lang: lang, Requested: time.Now().Unix()}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not write the request: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requested": true})
}

func (s *server) cancelAddAudio(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	lang := strings.TrimSpace(r.FormValue("lang"))
	movieID, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("movie_id")), 10, 64)
	if err != nil || lang == "" || strings.ContainsAny(lang, `/\.`) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "movie_id and lang are required"})
		return
	}

	s.dubMu.Lock()
	defer s.dubMu.Unlock()
	active, err := s.activeDubJob(movieID, lang)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if active {
		if err := s.writeDubRequest(dubRequest{MovieID: movieID, Lang: lang, Requested: time.Now().Unix(), Cancel: true}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not write the request: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	// dub-fetch has not taken the request yet, so dropping the file is the whole cancel.
	if err := os.Remove(s.dubRequestFile(movieID, lang)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "nothing to cancel"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
