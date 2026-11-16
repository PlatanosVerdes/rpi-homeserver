package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The page's side of Add audio: what it shows, and the requests it makes of the service (dubs.go).

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

func writeJSONFile(file string, payload any) error {
	raw, err := json.MarshalIndent(payload, "", " ")
	if err != nil {
		return err
	}
	return writeAtomic(file, raw)
}

// dubTarget is the film and language a request names, checked against what Radarr listed.
func (s *server) dubTarget(r *http.Request) (dubFilm, string, int, error) {
	lang := strings.TrimSpace(r.FormValue("lang"))
	movieID, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("movie_id")), 10, 64)
	if err != nil {
		return dubFilm{}, "", http.StatusBadRequest, errors.New("movie_id must be a number")
	}
	if _, ok := s.dubs.langs.byCode[lang]; !ok {
		return dubFilm{}, "", http.StatusBadRequest, fmt.Errorf("unknown language %q", lang)
	}
	s.dubs.mu.Lock()
	film, ok := s.dubs.film(movieID, lang)
	s.dubs.mu.Unlock()
	if !ok {
		return dubFilm{}, "", http.StatusBadRequest, errors.New("that film is not listed as missing that language")
	}
	return film, lang, 0, nil
}

func postOnly(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func (s *server) addAudio(w http.ResponseWriter, r *http.Request) {
	if !postOnly(w, r) {
		return
	}
	film, lang, code, err := s.dubTarget(r)
	if err == nil {
		code, err = s.dubs.requestAdd(film.ID, lang)
	}
	if err != nil {
		writeJSON(w, code, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requested": true})
}

func (s *server) cancelAddAudio(w http.ResponseWriter, r *http.Request) {
	if !postOnly(w, r) {
		return
	}
	lang := strings.TrimSpace(r.FormValue("lang"))
	movieID, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("movie_id")), 10, 64)
	if err != nil || lang == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "movie_id and lang are required"})
		return
	}
	if code, err := s.dubs.requestCancel(movieID, lang); err != nil {
		writeJSON(w, code, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) checkAddAudio(w http.ResponseWriter, r *http.Request) {
	if !postOnly(w, r) {
		return
	}
	film, lang, code, err := s.dubTarget(r)
	if err != nil {
		writeJSON(w, code, map[string]any{"error": err.Error()})
		return
	}
	if !s.dubs.checks.start(checkJob{key: dubKey(film.ID, lang), imdb: film.IMDb, title: film.Title, lang: lang}) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "already being checked"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checking": true})
}

// checkProgress is what the page polls every second while a check is queued or running.
func (s *server) checkProgress(w http.ResponseWriter, r *http.Request) {
	checks, _ := s.dubs.checks.snapshot()
	writeJSON(w, http.StatusOK, map[string]any{"checks": checks})
}
