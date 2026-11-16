package dubs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// gatedSearch is a check's search that reports one indexer, then waits for its gate to open (or the
// context to end) and answers what result and err say.
type gatedSearch struct {
	mu     sync.Mutex
	gates  map[string]chan struct{}
	result dubCheck
	err    error
}

func newGatedSearch(keys ...string) *gatedSearch {
	g := &gatedSearch{gates: map[string]chan struct{}{}}
	for _, key := range keys {
		g.gates[key] = make(chan struct{})
	}
	return g
}

func (g *gatedSearch) open(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	close(g.gates[key])
}

func (g *gatedSearch) search(ctx context.Context, job checkJob, progress searchProgress) (dubCheck, error) {
	progress("NOBS", 1, 2, 3, nil)
	g.mu.Lock()
	gate := g.gates[job.key]
	g.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return dubCheck{}, ctx.Err()
		}
	}
	progress("Public", 2, 2, 0, errors.New("timed out"))
	return g.result, g.err
}

// waitFor polls until the check under key satisfies done, or fails the test.
func waitFor(t *testing.T, c *checker, key string, done func(dubCheck) bool) dubCheck {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		checks, _ := c.snapshot()
		if check, ok := checks[key]; ok && done(check) {
			return check
		}
		time.Sleep(5 * time.Millisecond)
	}
	checks, _ := c.snapshot()
	t.Fatalf("%s never got there: %+v", key, checks[key])
	return dubCheck{}
}

func finished(c dubCheck) bool { return c.finished() }

func TestCheckRunner(t *testing.T) {
	t.Parallel()
	best := []checkRelease{{"X.2001.SPANiSH", 7, "NOBS", true}}
	cases := []struct {
		name   string
		result dubCheck
		err    error
		want   dubCheck
	}{
		{"result", dubCheck{Releases: 3, Named: 1, OwnSite: 2, Best: best}, nil,
			dubCheck{Status: "done", Done: 2, Total: 2, Releases: 3, Named: 1, OwnSite: 2, Best: best,
				Indexers: []checkIndexer{{"NOBS", 3, ""}, {"Public", 0, "timed out"}}}},
		{"error", dubCheck{}, errors.New("Prowlarr is down"),
			dubCheck{Status: "failed", Done: 2, Total: 2, Releases: 3, Error: "Prowlarr is down",
				Indexers: []checkIndexer{{"NOBS", 3, ""}, {"Public", 0, "timed out"}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			search := newGatedSearch()
			search.result, search.err = c.result, c.err
			checks := newChecker(filepath.Join(t.TempDir(), "checks.json"), search.search)
			if !checks.start(checkJob{key: "11-spa"}) {
				t.Fatal("not started")
			}
			got := waitFor(t, checks, "11-spa", finished)
			if got.Started == 0 || got.Time == 0 || got.Queued == 0 {
				t.Errorf("times not set: %+v", got)
			}
			got.Queued, got.Started, got.Time = 0, 0, 0
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got  %+v\nwant %+v", got, c.want)
			}
		})
	}
}

func TestCheckRunnerShowsProgress(t *testing.T) {
	t.Parallel()
	search := newGatedSearch("11-spa")
	checks := newChecker(filepath.Join(t.TempDir(), "checks.json"), search.search)
	checks.start(checkJob{key: "11-spa"})
	running := waitFor(t, checks, "11-spa", func(c dubCheck) bool { return c.Done == 1 })
	if running.Status != "running" || running.Total != 2 || running.Releases != 3 || len(running.Indexers) != 1 {
		t.Errorf("progress: %+v", running)
	}
	if checks.start(checkJob{key: "11-spa"}) {
		t.Error("a running check started twice")
	}
	search.open("11-spa")
	waitFor(t, checks, "11-spa", finished)
	if !checks.start(checkJob{key: "11-spa"}) {
		t.Error("a finished check cannot run again")
	}
	waitFor(t, checks, "11-spa", finished)
}

func TestCheckRunnerLimitsAndOrders(t *testing.T) {
	t.Parallel()
	search := newGatedSearch("a", "b", "c", "d")
	checks := newChecker(filepath.Join(t.TempDir(), "checks.json"), search.search)
	for _, key := range []string{"a", "b", "c", "d"} {
		checks.start(checkJob{key: key})
	}
	status := func() string {
		all, _ := checks.snapshot()
		return strings.Join([]string{all["a"].Status, all["b"].Status, all["c"].Status, all["d"].Status}, " ")
	}
	if got := status(); got != "running running queued queued" {
		t.Fatalf("at most two at once, in order: %s", got)
	}
	search.open("b")
	waitFor(t, checks, "c", func(c dubCheck) bool { return c.Status == "running" })
	if got := status(); got != "running done running queued" {
		t.Errorf("the first queued should take the free slot: %s", got)
	}
	for _, key := range []string{"a", "c", "d"} {
		search.open(key)
	}
	waitFor(t, checks, "d", finished)
}

func TestCheckRunnerTimesOut(t *testing.T) {
	t.Parallel()
	checks := newChecker(filepath.Join(t.TempDir(), "checks.json"), newGatedSearch("11-spa").search)
	checks.timeout = 20 * time.Millisecond
	checks.start(checkJob{key: "11-spa"})
	if got := waitFor(t, checks, "11-spa", finished); got.Status != "failed" || !strings.HasPrefix(got.Error, "no answer after") {
		t.Errorf("timeout: %+v", got)
	}
}

func TestChecksPersist(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "checks.json")
	search := newGatedSearch()
	search.result = dubCheck{Releases: 2, Named: 1, Best: []checkRelease{{"T", 1, "I", false}}}
	checks := newChecker(file, search.search)
	checks.start(checkJob{key: "11-spa"})
	want := waitFor(t, checks, "11-spa", finished)

	reloaded := newChecker(file, nil)
	reloaded.load()
	got, err := reloaded.snapshot()
	if err != nil || !reflect.DeepEqual(got["11-spa"], want) {
		t.Errorf("round trip: %+v %v, want %+v", got["11-spa"], err, want)
	}

	// A check from before they had a status.
	os.WriteFile(file, []byte(`{"11-spa":{"time":5,"releases":4,"named":1},"12-spa":{"time":5,"error":"down"}}`), 0o644)
	older := newChecker(file, nil)
	older.load()
	got, _ = older.snapshot()
	if got["11-spa"].Status != "done" || got["11-spa"].Named != 1 || got["12-spa"].Status != "failed" {
		t.Errorf("older file: %+v", got)
	}

	os.WriteFile(file, []byte("["), 0o644)
	broken := newChecker(file, nil)
	broken.load()
	if _, err := broken.snapshot(); err == nil {
		t.Error("a malformed file must say so")
	}
}

func TestAddAudioCheck(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, movieID, lang string
		want                int
	}{
		{"unknown language", "11", "fre", http.StatusBadRequest},
		{"film not missing it", "13", "spa", http.StatusBadRequest},
		{"not a number", "x", "spa", http.StatusBadRequest},
		{"while a job runs", "12", "spa", http.StatusOK},
		{"new", "11", "spa", http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d := dubFixture(t)
			d.checks.search = newGatedSearch().search
			if code, body := dubPost(d.ServeCheck, c.movieID, c.lang); code != c.want {
				t.Fatalf("code %d, want %d: %v", code, c.want, body)
			}
		})
	}

	d := dubFixture(t)
	search := newGatedSearch("11-spa")
	search.result = dubCheck{Releases: 4, Named: 2}
	var asked checkJob
	d.checks.search = func(ctx context.Context, job checkJob, progress searchProgress) (dubCheck, error) {
		asked = job
		return search.search(ctx, job, progress)
	}
	if code, body := dubPost(d.ServeCheck, "11", "spa"); code != http.StatusOK {
		t.Fatalf("%d %v", code, body)
	}
	if code, _ := dubPost(d.ServeCheck, "11", "spa"); code != http.StatusConflict {
		t.Errorf("a second check of the same film and language: %d", code)
	}
	waitFor(t, d.checks, "11-spa", func(c dubCheck) bool { return c.Done == 1 })
	if asked != (checkJob{key: "11-spa", imdb: "tt0230600", title: "The Others", lang: "spa"}) {
		t.Errorf("searched for %+v", asked)
	}

	w := httptest.NewRecorder()
	d.ServeChecks(w, httptest.NewRequest("GET", "/api/add-audio/checks", nil))
	if !strings.Contains(w.Body.String(), `"11-spa":{"status":"running"`) {
		t.Errorf("progress: %s", w.Body)
	}
	search.open("11-spa")
	waitFor(t, d.checks, "11-spa", finished)

	got, raw := addAudioPayload(t, d)
	check, _ := got["checks"].(map[string]any)["11-spa"].(map[string]any)
	if check["status"] != "done" || check["named"] != 2.0 || len(got["requests"].([]any)) != 0 {
		t.Errorf("state after the check: %s", raw)
	}
}

func TestAddAudioChecksState(t *testing.T) {
	t.Parallel()
	d := dubFixture(t)
	file := filepath.Join(d.cfg.StateDir, "checks.json")
	os.WriteFile(file, []byte(`{
		"11-spa": {"status": "done", "time": 5, "releases": 4, "named": 1},
		"13-spa": {"status": "done", "time": 5, "releases": 1},
		"99-cat": {"status": "failed", "time": 5, "error": "gone"}}`), 0o644)
	d.checks.load()
	got, raw := addAudioPayload(t, d)
	if shown := got["checks"].(map[string]any); len(shown) != 1 || shown["11-spa"] == nil {
		t.Errorf("only listed films and languages: %s", raw)
	}
	if _, has := got["error"]; has {
		t.Errorf("unexpected error: %v", got["error"])
	}

	os.WriteFile(file, []byte("["), 0o644)
	d.checks.load()
	if got, _ := addAudioPayload(t, d); got["error"] == nil {
		t.Error("a malformed checks.json must show as an error")
	}
}
