package dubs

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"audio-manager/internal/httpx"
)

// The page's side of Add audio: the requests it makes of the service (dubs.go).

// target is the film and language a request names, checked against what Radarr listed.
func (d *Service) target(r *http.Request) (dubFilm, string, int, error) {
	lang := strings.TrimSpace(r.FormValue("lang"))
	movieID, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("movie_id")), 10, 64)
	if err != nil {
		return dubFilm{}, "", http.StatusBadRequest, errors.New("movie_id must be a number")
	}
	if _, ok := d.langs.byCode[lang]; !ok {
		return dubFilm{}, "", http.StatusBadRequest, fmt.Errorf("unknown language %q", lang)
	}
	d.mu.Lock()
	film, ok := d.film(movieID, lang)
	d.mu.Unlock()
	if !ok {
		return dubFilm{}, "", http.StatusBadRequest, errors.New("that film is not listed as missing that language")
	}
	return film, lang, 0, nil
}

func (d *Service) ServeAdd(w http.ResponseWriter, r *http.Request) {
	if !httpx.PostOnly(w, r) {
		return
	}
	film, lang, code, err := d.target(r)
	if err == nil {
		code, err = d.requestAdd(film.ID, lang)
	}
	if err != nil {
		httpx.WriteJSON(w, code, map[string]any{"error": err.Error()})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"requested": true})
}

func (d *Service) ServeCancel(w http.ResponseWriter, r *http.Request) {
	if !httpx.PostOnly(w, r) {
		return
	}
	lang := strings.TrimSpace(r.FormValue("lang"))
	movieID, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("movie_id")), 10, 64)
	if err != nil || lang == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "movie_id and lang are required"})
		return
	}
	if code, err := d.requestCancel(movieID, lang); err != nil {
		httpx.WriteJSON(w, code, map[string]any{"error": err.Error()})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (d *Service) ServeCheck(w http.ResponseWriter, r *http.Request) {
	if !httpx.PostOnly(w, r) {
		return
	}
	film, lang, code, err := d.target(r)
	if err != nil {
		httpx.WriteJSON(w, code, map[string]any{"error": err.Error()})
		return
	}
	if !d.checks.start(checkJob{key: dubKey(film.ID, lang), imdb: film.IMDb, title: film.Title, lang: lang}) {
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{"error": "already being checked"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"checking": true})
}

// ServeChecks is what the page polls every second while a check is queued or running.
func (d *Service) ServeChecks(w http.ResponseWriter, r *http.Request) {
	checks, _ := d.checks.snapshot()
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"checks": checks})
}
