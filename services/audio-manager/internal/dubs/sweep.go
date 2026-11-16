package dubs

import (
	"log"
	"sort"
	"time"
)

// The sweep searches one film per tick in the background, so the page knows which films have a
// release in a language before anyone asks: films never searched first, then the oldest search once
// it is older than the refresh. It only starts one when nothing else is asking Prowlarr.

// retryAfter is how long a search that failed waits before the sweep tries that film again.
const retryAfter = time.Hour

type sweepStatus struct {
	Searched  int    `json:"searched"`
	Total     int    `json:"total"`
	Next      int64  `json:"next_id,omitempty"`
	NextTitle string `json:"next_title,omitempty"`
	NextAt    int64  `json:"next_at,omitempty"`
}

// due is when the sweep should search a film next: at once if it never was.
func due(search filmSearch, found bool, refresh time.Duration) int64 {
	if !found {
		return 0
	}
	at := int64(0)
	if search.Time > 0 {
		at = search.Time + int64(refresh/time.Second)
	}
	if search.Failed > 0 {
		at = max(at, search.Failed+int64(retryAfter/time.Second))
	}
	return at
}

// nextToSweep is the film whose search is due first, in the list's order among equals.
func nextToSweep(films []dubFilm, done map[int64]filmSearch, refresh time.Duration) (dubFilm, int64, bool) {
	if len(films) == 0 {
		return dubFilm{}, 0, false
	}
	order := make([]int, len(films))
	dueAt := make([]int64, len(films))
	for i, f := range films {
		search, found := done[f.ID]
		order[i], dueAt[i] = i, due(search, found, refresh)
	}
	sort.SliceStable(order, func(a, b int) bool { return dueAt[order[a]] < dueAt[order[b]] })
	first := order[0]
	return films[first], dueAt[first], true
}

func (d *Service) sweepLoop() {
	ticker := time.NewTicker(d.cfg.SweepEvery)
	defer ticker.Stop()
	for {
		d.sweep(time.Now())
		<-ticker.C
	}
}

// sweep is one tick: it starts the search of the film due first, unless Prowlarr is already busy
// with a check, a job's search or the previous tick's.
func (d *Service) sweep(now time.Time) {
	d.mu.Lock()
	d.lastSweep = now.Unix()
	films := d.films
	listed := d.filmsTime != 0
	d.mu.Unlock()
	if !listed || d.jobSearching.Load() || d.searches.busy() {
		return
	}
	done, _, _ := d.searches.snapshot()
	film, dueAt, ok := nextToSweep(films, done, d.cfg.SweepRefresh)
	if !ok || dueAt > now.Unix() {
		return
	}
	if d.searches.start(searchJob{id: film.ID, imdb: film.IMDb, title: film.Title, sweep: true}) {
		log.Printf("audio-manager: sweep: searching %s (%d)", film.Title, film.Year)
	}
}

// sweepStatus says how far the sweep is, and which film it searches next and when.
func (d *Service) sweepStatus(films []dubFilm, done map[int64]filmSearch, lastSweep int64) sweepStatus {
	status := sweepStatus{Total: len(films)}
	for _, f := range films {
		if search, ok := done[f.ID]; ok && search.Time > 0 {
			status.Searched++
		}
	}
	if film, dueAt, ok := nextToSweep(films, done, d.cfg.SweepRefresh); ok {
		status.Next, status.NextTitle = film.ID, film.Title
		status.NextAt = max(dueAt, lastSweep+int64(d.cfg.SweepEvery/time.Second))
	}
	return status
}
