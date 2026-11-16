package dubs

import (
	"audio-manager/internal/store"
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"sync"
	"time"
)

// A check asks every indexer what it has for one film in one language and downloads nothing: the
// same search a job starts with, its progress shown as each indexer answers.

const (
	checksAtOnce = 2
	checkTimeout = 6 * time.Minute
)

type checkIndexer struct {
	Name     string `json:"name"`
	Releases int    `json:"releases"`
	Error    string `json:"error,omitempty"`
}

type checkRelease struct {
	Title   string `json:"title"`
	Size    int64  `json:"size"`
	Indexer string `json:"indexer"`
	Private bool   `json:"private"`
}

type dubCheck struct {
	Status   string         `json:"status"` // queued, running, done, failed
	Queued   int64          `json:"queued"`
	Started  int64          `json:"started,omitempty"`
	Time     int64          `json:"time,omitempty"` // when it finished
	Done     int            `json:"done"`
	Total    int            `json:"total"`
	Indexers []checkIndexer `json:"indexers,omitempty"`
	Releases int            `json:"releases"`
	Named    int            `json:"named"`
	OwnSite  int            `json:"own_site"`
	Best     []checkRelease `json:"best,omitempty"`
	Error    string         `json:"error,omitempty"`
}

func (c dubCheck) finished() bool { return c.Status == "done" || c.Status == "failed" }

type checkJob struct{ key, imdb, title, lang string }

// checkSearch is the search a check runs; Service.check in the service, a fake in tests.
type checkSearch func(ctx context.Context, job checkJob, progress searchProgress) (dubCheck, error)

type checker struct {
	file    string
	search  checkSearch
	limit   int
	timeout time.Duration

	mu      sync.Mutex
	checks  map[string]dubCheck
	queue   []checkJob
	running int
	loadErr error
}

func newChecker(file string, search checkSearch) *checker {
	return &checker{file: file, search: search, limit: checksAtOnce, timeout: checkTimeout, checks: map[string]dubCheck{}}
}

// load reads the finished checks; one written before checks had a status gets one.
func (c *checker) load() {
	saved := map[string]dubCheck{}
	err := store.ReadJSON(c.file, &saved)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loadErr = err
	for key, check := range saved {
		if check.Status == "" {
			check.Status = "done"
			if check.Error != "" {
				check.Status = "failed"
			}
		}
		if check.finished() {
			c.checks[key] = check
		}
	}
}

// save is called with c.mu held.
func (c *checker) save() {
	finished := map[string]dubCheck{}
	for key, check := range c.checks {
		if check.finished() {
			finished[key] = check
		}
	}
	if err := store.WriteJSON(c.file, finished); err != nil {
		log.Printf("audio-manager: saving checks: %v", err)
	}
}

func (c *checker) snapshot() (map[string]dubCheck, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := maps.Clone(c.checks)
	for key, check := range out {
		check.Indexers = slices.Clone(check.Indexers)
		out[key] = check
	}
	return out, c.loadErr
}

// start queues a check, or says no when the same film and language is already queued or running.
func (c *checker) start(job checkJob) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if current, ok := c.checks[job.key]; ok && !current.finished() {
		return false
	}
	c.checks[job.key] = dubCheck{Status: "queued", Queued: time.Now().Unix()}
	c.queue = append(c.queue, job)
	c.pump()
	return true
}

// pump is called with c.mu held.
func (c *checker) pump() {
	for c.running < c.limit && len(c.queue) > 0 {
		job := c.queue[0]
		c.queue = c.queue[1:]
		c.running++
		check := c.checks[job.key]
		check.Status, check.Started = "running", time.Now().Unix()
		c.checks[job.key] = check
		go c.run(job)
	}
}

func (c *checker) run(job checkJob) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	result, err := c.search(ctx, job, func(name string, done, total, releases int, err error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		check := c.checks[job.key]
		check.Done, check.Total = done, total
		check.Releases += releases
		site := checkIndexer{Name: name, Releases: releases}
		if err != nil {
			site.Error = err.Error()
		}
		check.Indexers = append(check.Indexers, site)
		c.checks[job.key] = check
	})
	if errors.Is(err, context.DeadlineExceeded) {
		err = fmt.Errorf("no answer after %v", c.timeout)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	check := c.checks[job.key]
	if err != nil {
		check.Status, check.Error = "failed", err.Error()
	} else {
		check.Status, check.Error = "done", ""
		check.Releases, check.Named, check.OwnSite, check.Best = result.Releases, result.Named, result.OwnSite, result.Best
	}
	check.Time = time.Now().Unix()
	c.checks[job.key] = check
	c.running--
	c.save()
	c.pump()
}

// check is the service's search for a check: how many releases could be probed, how many name the
// language, how many come from a site in it, and the three most likely.
func (d *Service) check(ctx context.Context, job checkJob, progress searchProgress) (dubCheck, error) {
	found, err := d.find(ctx, job.imdb, job.title, job.lang, nil, progress)
	if err != nil {
		return dubCheck{}, err
	}
	return summarise(found), nil
}

func summarise(found []candidate) dubCheck {
	result := dubCheck{Releases: len(found), Best: []checkRelease{}}
	for i, c := range found {
		if c.Named {
			result.Named++
		} else if c.OwnSite {
			result.OwnSite++
		}
		if i < 3 {
			result.Best = append(result.Best, checkRelease{Title: c.Title, Size: c.Size, Indexer: c.Indexer, Private: c.Private})
		}
	}
	return result
}
