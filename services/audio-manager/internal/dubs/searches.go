package dubs

import (
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"sync"
	"time"

	"audio-manager/internal/store"
)

// A film's search asks every indexer what it has and downloads nothing. What it finds does not depend
// on the language, so each film is searched once and every language is ranked from the cached releases.

const (
	searchesAtOnce    = 2
	filmSearchTimeout = 6 * time.Minute
)

// filmSearch is the cached result of a film's last search. Failed and Error are a later search that
// did not finish; the releases stay those of the last one that did.
type filmSearch struct {
	Time     int64    `json:"time"`
	Indexers int      `json:"indexers_total"`
	Errors   []string `json:"errors,omitempty"`
	Releases []found  `json:"releases"`
	Failed   int64    `json:"failed,omitempty"`
	Error    string   `json:"error,omitempty"`
}

type checkIndexer struct {
	Name     string `json:"name"`
	Releases int    `json:"releases"`
	Error    string `json:"error,omitempty"`
}

// liveSearch is a search queued or under way, as the page follows it.
type liveSearch struct {
	Status   string         `json:"status"` // queued, running
	Queued   int64          `json:"queued"`
	Started  int64          `json:"started,omitempty"`
	Done     int            `json:"done"`
	Total    int            `json:"total"`
	Releases int            `json:"releases"`
	Indexers []checkIndexer `json:"indexers,omitempty"`
	Sweep    bool           `json:"sweep,omitempty"`
}

type searchJob struct {
	id          int64
	imdb, title string
	sweep       bool
}

// searchProgress is told about each indexer as it answers.
type searchProgress func(indexer string, done, total, releases int, err error)

// searchRun is a film's search; Service.searchFilm in the service, a fake in tests.
type searchRun func(ctx context.Context, job searchJob, progress searchProgress) (filmSearch, error)

// searches holds the cache and runs the searches that fill it, at most limit at once.
type searches struct {
	file    string
	run     searchRun
	limit   int
	timeout time.Duration

	mu      sync.Mutex
	done    map[int64]filmSearch
	live    map[int64]liveSearch
	queue   []searchJob
	running int
	// generation counts the changes to done, so what is worked out from it can be kept until the next.
	generation int
	loadErr    error
}

func newSearches(file string, run searchRun) *searches {
	return &searches{file: file, run: run, limit: searchesAtOnce, timeout: filmSearchTimeout,
		done: map[int64]filmSearch{}, live: map[int64]liveSearch{}}
}

func (s *searches) load() {
	saved := map[int64]filmSearch{}
	err := store.ReadJSON(s.file, &saved)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadErr = err
	if err == nil {
		s.done = saved
		s.generation++
	}
}

// save is called with s.mu held.
func (s *searches) save() {
	s.generation++
	if err := store.WriteJSON(s.file, s.done); err != nil {
		log.Printf("audio-manager: saving searches: %v", err)
	}
}

// snapshot shares the cached entries, which are replaced whole and never changed in place.
func (s *searches) snapshot() (done map[int64]filmSearch, generation int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.done), s.generation, s.loadErr
}

// underway is the searches queued or running.
func (s *searches) underway() map[int64]liveSearch {
	s.mu.Lock()
	defer s.mu.Unlock()
	live := maps.Clone(s.live)
	for id, search := range live {
		search.Indexers = slices.Clone(search.Indexers)
		live[id] = search
	}
	return live
}

func (s *searches) busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.live) > 0
}

// start queues a search, or says no when the film is already queued or being searched.
func (s *searches) start(job searchJob) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.live[job.id]; ok {
		return false
	}
	s.live[job.id] = liveSearch{Status: "queued", Queued: time.Now().Unix(), Sweep: job.sweep}
	s.queue = append(s.queue, job)
	s.pump()
	return true
}

// pump is called with s.mu held.
func (s *searches) pump() {
	for s.running < s.limit && len(s.queue) > 0 {
		job := s.queue[0]
		s.queue = s.queue[1:]
		s.running++
		search := s.live[job.id]
		search.Status, search.Started = "running", time.Now().Unix()
		s.live[job.id] = search
		go s.runOne(job)
	}
}

func (s *searches) runOne(job searchJob) {
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	result, err := s.run(ctx, job, func(name string, done, total, releases int, err error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		search := s.live[job.id]
		search.Done, search.Total = done, total
		search.Releases += releases
		site := checkIndexer{Name: name, Releases: releases}
		if err != nil {
			site.Error = err.Error()
		}
		search.Indexers = append(search.Indexers, site)
		s.live[job.id] = search
	})
	if errors.Is(err, context.DeadlineExceeded) {
		err = fmt.Errorf("no answer after %v", s.timeout)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.live, job.id)
	s.running--
	s.record(job.id, result, err)
	s.pump()
}

// put stores a search a job ran itself.
func (s *searches) put(id int64, result filmSearch) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.record(id, result, nil)
}

// record is called with s.mu held.
func (s *searches) record(id int64, result filmSearch, err error) {
	if err != nil {
		last := s.done[id]
		last.Failed, last.Error = time.Now().Unix(), err.Error()
		s.done[id] = last
	} else {
		result.Releases = slices.Clone(result.Releases)
		for i := range result.Releases {
			result.Releases[i].url = ""
		}
		s.done[id] = result
	}
	s.save()
}

// keepOnly drops the films Radarr no longer lists.
func (s *searches) keepOnly(films []dubFilm) {
	listed := map[int64]bool{}
	for _, f := range films {
		listed[f.ID] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	before := len(s.done)
	maps.DeleteFunc(s.done, func(id int64, _ filmSearch) bool { return !listed[id] })
	if len(s.done) != before {
		s.save()
	}
}
