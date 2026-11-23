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

// gatedSearch is a film's search that reports one indexer, then waits for its gate to open (or the
// context to end) and answers what result and err say.
type gatedSearch struct {
	mu     sync.Mutex
	gates  map[int64]chan struct{}
	result filmSearch
	err    error
}

func newGatedSearch(ids ...int64) *gatedSearch {
	g := &gatedSearch{gates: map[int64]chan struct{}{}}
	for _, id := range ids {
		g.gates[id] = make(chan struct{})
	}
	return g
}

func (g *gatedSearch) open(id int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	close(g.gates[id])
}

func (g *gatedSearch) search(ctx context.Context, job searchJob, progress searchProgress) (filmSearch, error) {
	progress("NOBS", 1, 2, 3, nil)
	g.mu.Lock()
	gate := g.gates[job.id]
	g.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return filmSearch{}, ctx.Err()
		}
	}
	progress("Public", 2, 2, 0, errors.New("timed out"))
	return g.result, g.err
}

// waitFor polls until done says the film's search got there, or fails the test.
func waitFor(t *testing.T, s *searches, id int64, done func(live liveSearch, underway bool, cached filmSearch) bool) filmSearch {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		live, underway := s.underway()[id]
		cached, _, _ := s.snapshot()
		if done(live, underway, cached[id]) {
			return cached[id]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("film %d never got there: %+v", id, s.underway()[id])
	return filmSearch{}
}

func finished(_ liveSearch, underway bool, _ filmSearch) bool { return !underway }

func releasesNamed(titles ...string) []found {
	var out []found
	for i, title := range titles {
		out = append(out, found{GUID: string(rune('a' + i)), Title: title, Size: gb, Seeders: 3, Indexer: "Public", SiteLanguage: "en-US"})
	}
	return out
}

func TestSearchRunner(t *testing.T) {
	t.Parallel()
	earlier := filmSearch{Time: 5, Indexers: 2, Releases: releasesNamed("Film Castellano")}
	cases := []struct {
		name   string
		result filmSearch
		err    error
		want   filmSearch
	}{
		{"a result replaces the last", filmSearch{Time: 9, Indexers: 2, Releases: releasesNamed("Film MULTi")}, nil,
			filmSearch{Time: 9, Indexers: 2, Releases: releasesNamed("Film MULTi")}},
		{"a failure keeps the last result", filmSearch{}, errors.New("Prowlarr is down"),
			filmSearch{Time: 5, Indexers: 2, Releases: releasesNamed("Film Castellano"), Error: "Prowlarr is down"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			search := newGatedSearch()
			search.result, search.err = c.result, c.err
			s := newSearches(filepath.Join(t.TempDir(), "searches.json"), search.search)
			s.done[11] = earlier
			if !s.start(searchJob{id: 11}) {
				t.Fatal("not started")
			}
			got := waitFor(t, s, 11, finished)
			if (c.err != nil) != (got.Failed > 0) {
				t.Errorf("failed at %d", got.Failed)
			}
			got.Failed = 0
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got  %+v\nwant %+v", got, c.want)
			}
		})
	}
}

func TestSearchShowsProgress(t *testing.T) {
	t.Parallel()
	search := newGatedSearch(11)
	s := newSearches(filepath.Join(t.TempDir(), "searches.json"), search.search)
	s.start(searchJob{id: 11})
	waitFor(t, s, 11, func(live liveSearch, _ bool, _ filmSearch) bool { return live.Done == 1 })
	running := s.underway()[11]
	if running.Status != "running" || running.Total != 2 || running.Releases != 3 || len(running.Indexers) != 1 || !s.busy() {
		t.Errorf("progress: %+v", running)
	}
	if s.start(searchJob{id: 11}) {
		t.Error("a running search started twice")
	}
	search.open(11)
	waitFor(t, s, 11, finished)
	if s.busy() || !s.start(searchJob{id: 11}) {
		t.Error("a finished search cannot run again")
	}
	waitFor(t, s, 11, finished)
}

func TestSearchesLimitAndOrder(t *testing.T) {
	t.Parallel()
	search := newGatedSearch(1, 2, 3, 4)
	s := newSearches(filepath.Join(t.TempDir(), "searches.json"), search.search)
	for id := range int64(4) {
		s.start(searchJob{id: id + 1})
	}
	status := func() string {
		live := s.underway()
		var out []string
		for id := range int64(4) {
			if l, ok := live[id+1]; ok {
				out = append(out, l.Status)
			} else {
				out = append(out, "done")
			}
		}
		return strings.Join(out, " ")
	}
	if got := status(); got != "running running queued queued" {
		t.Fatalf("at most two at once, in order: %s", got)
	}
	search.open(2)
	waitFor(t, s, 3, func(live liveSearch, _ bool, _ filmSearch) bool { return live.Status == "running" })
	if got := status(); got != "running done running queued" {
		t.Errorf("the first queued should take the free slot: %s", got)
	}
	for _, id := range []int64{1, 3, 4} {
		search.open(id)
	}
	waitFor(t, s, 4, finished)
}

func TestSearchTimesOut(t *testing.T) {
	t.Parallel()
	s := newSearches(filepath.Join(t.TempDir(), "searches.json"), newGatedSearch(11).search)
	s.timeout = 20 * time.Millisecond
	s.start(searchJob{id: 11})
	if got := waitFor(t, s, 11, finished); got.Failed == 0 || !strings.HasPrefix(got.Error, "no answer after") {
		t.Errorf("timeout: %+v", got)
	}
}

func TestSearchesPersistWithoutLinks(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "searches.json")
	s := newSearches(file, nil)
	releases := releasesNamed("Film Castellano")
	releases[0].url = "http://prowlarr/1/download?link=secret"
	s.put(11, filmSearch{Time: 7, Indexers: 3, Errors: []string{"X: down"}, Releases: releases})

	raw, _ := os.ReadFile(file)
	if strings.Contains(string(raw), "secret") || !strings.Contains(string(raw), `"indexers_total": 3`) {
		t.Errorf("the cache keeps a link or lacks a field: %s", raw)
	}
	reloaded := newSearches(file, nil)
	reloaded.load()
	got, generation, err := reloaded.snapshot()
	releases[0].url = ""
	want := filmSearch{Time: 7, Indexers: 3, Errors: []string{"X: down"}, Releases: releases}
	if err != nil || generation == 0 || !reflect.DeepEqual(got[11], want) {
		t.Errorf("round trip: %+v %v, want %+v", got[11], err, want)
	}

	os.WriteFile(file, []byte("["), 0o644)
	broken := newSearches(file, nil)
	broken.load()
	if _, _, err := broken.snapshot(); err == nil {
		t.Error("a malformed file must say so")
	}
}

func TestFilmsThatLeaveRadarrLeaveTheCache(t *testing.T) {
	t.Parallel()
	d := dubFixture(t)
	d.searches.put(11, filmSearch{Time: 1})
	d.searches.put(99, filmSearch{Time: 1})
	radarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id": 11, "title": "The Others", "year": 2001, "imdbId": "tt0230600", "hasFile": true,
			"movieFile": {"path": "/data/films/O/o.mkv", "mediaInfo": {"audioLanguages": "eng"}}}]`))
	}))
	defer radarr.Close()
	d.cfg.RadarrURL = radarr.URL
	d.listFilms()
	done, _, _ := d.searches.snapshot()
	if _, kept := done[11]; !kept || len(done) != 1 {
		t.Errorf("cache after Radarr dropped film 99: %+v", done)
	}
	saved := newSearches(d.searches.file, nil)
	saved.load()
	if got, _, _ := saved.snapshot(); len(got) != 1 {
		t.Errorf("the drop was not saved: %+v", got)
	}
}

func TestAvailabilityPerLanguage(t *testing.T) {
	t.Parallel()
	langs, rules := testLanguages(t), testRules(t)
	search := filmSearch{Time: 7, Indexers: 4, Errors: []string{"X: down"}, Releases: []found{
		{GUID: "named", Title: "Film 2000 Castellano", Size: 2 * gb, Seeders: 1, Indexer: "Public", SiteLanguage: "en-US"},
		{GUID: "site", Title: "Film 2000", Size: gb, Seeders: 1, Indexer: "NOBS", Private: true, SiteLanguage: "es-ES"},
		{GUID: "latin", Title: "Film 2000 Latino", Size: gb, Seeders: 1, Indexer: "Public", SiteLanguage: "en-US"},
		{GUID: "plain", Title: "Film 2000 1080p", Size: gb, Seeders: 1, Indexer: "Public", SiteLanguage: "en-US"},
	}}
	cases := []struct {
		lang                     string
		releases, named, ownSite int
		best                     string
	}{
		{"spa", 3, 1, 1, "Film 2000 Castellano"},
		{"cat", 4, 0, 0, "Film 2000 Latino"},
	}
	for _, c := range cases {
		t.Run(c.lang, func(t *testing.T) {
			t.Parallel()
			got := availabilityOf(search, langs.byCode[c.lang], rules)
			if got.Releases != c.releases || got.Named != c.named || got.OwnSite != c.ownSite || got.Best[0].Title != c.best ||
				got.Time != 7 || got.Indexers != 4 || got.Failing != 1 {
				t.Errorf("%+v", got)
			}
		})
	}

	films := []dubFilm{{ID: 1, Missing: []string{"spa", "cat"}}, {ID: 2, Missing: []string{"spa"}}, {ID: 3, Missing: []string{"cat"}}}
	counts := availableCounts(films, map[int64]filmSearch{1: search, 2: {Time: 1}, 3: {Time: 1}}, langs, rules)
	if counts["spa"] != 1 || counts["cat"] != 0 {
		t.Errorf("counts %v", counts)
	}
	failed := search
	failed.Failed, failed.Error = 9, "no answer after 6m0s"
	if got := availabilityOf(failed, langs.byCode["spa"], rules); got.Error == "" || got.Named != 1 {
		t.Errorf("a failed refresh keeps the counts and says so: %+v", got)
	}
}

func TestNextToSweep(t *testing.T) {
	t.Parallel()
	week := 7 * 24 * time.Hour
	films := []dubFilm{{ID: 1}, {ID: 2}, {ID: 3}}
	weekSeconds := int64(week / time.Second)
	cases := []struct {
		name   string
		done   map[int64]filmSearch
		want   int64
		wantAt int64
	}{
		{"never searched first, in the list's order", map[int64]filmSearch{1: {Time: 100}}, 2, 0},
		{"then the oldest", map[int64]filmSearch{1: {Time: 300}, 2: {Time: 100}, 3: {Time: 200}}, 2, 100 + weekSeconds},
		{"a failure waits an hour", map[int64]filmSearch{1: {Time: 300}, 2: {Failed: 50}, 3: {Time: 200}}, 2, 50 + 3600},
		{"a failed refresh does not jump the queue", map[int64]filmSearch{1: {Time: 300, Failed: 300 + weekSeconds},
			2: {Time: 400}, 3: {Time: 500}}, 2, 400 + weekSeconds},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			film, at, ok := nextToSweep(films, c.done, week)
			if !ok || film.ID != c.want || at != c.wantAt {
				t.Errorf("next %d at %d, want %d at %d", film.ID, at, c.want, c.wantAt)
			}
		})
	}
	if _, _, ok := nextToSweep(nil, nil, week); ok {
		t.Error("nothing to sweep without films")
	}
}

func TestSweep(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_000_000, 0)
	fresh := filmSearch{Time: now.Unix() - 60}
	cases := []struct {
		name      string
		prepare   func(d *Service, gate *gatedSearch)
		wantStart int64
	}{
		{"searches the film due first", func(d *Service, _ *gatedSearch) {
			d.searches.done = map[int64]filmSearch{11: fresh, 13: fresh}
		}, 12},
		{"leaves Prowlarr alone while a check runs", func(d *Service, gate *gatedSearch) {
			d.searches.start(searchJob{id: 13})
		}, 0},
		{"or while a job searches", func(d *Service, _ *gatedSearch) { d.jobSearching.Store(true) }, 0},
		{"nothing before Radarr has listed the films", func(d *Service, _ *gatedSearch) { d.filmsTime = 0 }, 0},
		{"nothing while every search is fresh", func(d *Service, _ *gatedSearch) {
			d.searches.done = map[int64]filmSearch{11: fresh, 12: fresh, 13: fresh}
		}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d := dubFixture(t)
			d.state.Jobs = map[string]*jobState{}
			gate := newGatedSearch(11, 12, 13)
			d.searches.run = gate.search
			c.prepare(d, gate)
			before := d.searches.underway()
			d.sweep(now)
			var started []int64
			for id, live := range d.searches.underway() {
				if _, already := before[id]; !already {
					started = append(started, id)
					if !live.Sweep {
						t.Errorf("film %d not marked as the sweep's", id)
					}
				}
			}
			if (c.wantStart == 0 && len(started) > 0) || (c.wantStart != 0 && (len(started) != 1 || started[0] != c.wantStart)) {
				t.Errorf("started %v, want %d", started, c.wantStart)
			}
			for _, id := range []int64{11, 12, 13} {
				gate.open(id)
			}
		})
	}
}

func TestSweepStatus(t *testing.T) {
	t.Parallel()
	d := dubFixture(t)
	d.cfg.SweepEvery, d.cfg.SweepRefresh = 5*time.Minute, 7*24*time.Hour
	d.state.Jobs = map[string]*jobState{}
	status := d.sweepStatus(d.films, map[int64]filmSearch{11: {Time: 100}, 13: {Failed: 100}}, 1000)
	want := sweepStatus{Searched: 1, Total: 2, Next: 12, NextTitle: "Heat", NextAt: 1300}
	if status != want {
		t.Errorf("got %+v, want %+v", status, want)
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
			d.searches.run = newGatedSearch().search
			if code, body := dubPost(d.ServeCheck, c.movieID, c.lang); code != c.want {
				t.Fatalf("code %d, want %d: %v", code, c.want, body)
			}
		})
	}

	d := dubFixture(t)
	search := newGatedSearch(11)
	search.result = filmSearch{Time: 9, Indexers: 2, Releases: releasesNamed("The Others 2001 Castellano", "The Others 2001")}
	var asked searchJob
	d.searches.run = func(ctx context.Context, job searchJob, progress searchProgress) (filmSearch, error) {
		asked = job
		return search.search(ctx, job, progress)
	}
	if code, body := dubPost(d.ServeCheck, "11", "spa"); code != http.StatusOK {
		t.Fatalf("%d %v", code, body)
	}
	if code, _ := dubPost(d.ServeCheck, "11", "cat"); code != http.StatusConflict {
		t.Errorf("a second search of the same film, in any language: %d", code)
	}
	waitFor(t, d.searches, 11, func(live liveSearch, _ bool, _ filmSearch) bool { return live.Done == 1 })
	if asked != (searchJob{id: 11, imdb: "tt0230600", title: "The Others"}) {
		t.Errorf("searched for %+v", asked)
	}

	w := httptest.NewRecorder()
	d.ServeChecks(w, httptest.NewRequest("GET", "/api/add-audio/checks", nil))
	if !strings.Contains(w.Body.String(), `"11":{"status":"running"`) {
		t.Errorf("progress: %s", w.Body)
	}
	got, _ := addAudioPayload(t, d)
	if _, shown := got["searching"].(map[string]any)["11"]; !shown {
		t.Errorf("the state does not show the search under way: %v", got["searching"])
	}
	search.open(11)
	waitFor(t, d.searches, 11, finished)

	got, raw := addAudioPayload(t, d)
	film, _ := got["availability"].(map[string]any)["11"].(map[string]any)
	if got["lang"] != "spa" || film["named"] != 1.0 || film["releases"] != 2.0 || got["available_counts"].(map[string]any)["spa"] != 1.0 {
		t.Errorf("state after the search: %s", raw)
	}
}

func TestAddAudioStateWithoutSearches(t *testing.T) {
	t.Parallel()
	d := dubFixture(t)
	got, raw := addAudioPayload(t, d)
	if len(got["availability"].(map[string]any)) != 0 || len(got["searching"].(map[string]any)) != 0 {
		t.Errorf("nothing searched yet: %s", raw)
	}
	if sweep := got["sweep"].(map[string]any); sweep["total"] != 1.0 || sweep["searched"] != 0.0 || sweep["next_title"] != "The Others" {
		t.Errorf("sweep: %v", sweep)
	}
	if _, has := got["error"]; has {
		t.Errorf("unexpected error: %v", got["error"])
	}

	os.WriteFile(d.searches.file, []byte("["), 0o644)
	d.searches.load()
	if got, _ := addAudioPayload(t, d); got["error"] == nil {
		t.Error("a malformed searches.json must show as an error")
	}
}

func TestToSweep(t *testing.T) {
	t.Parallel()
	castellano := filmSearch{Time: 7, Releases: []found{{GUID: "es", Title: "Film 2000 Castellano", Size: gb, Seeders: 1, Indexer: "Public", SiteLanguage: "en-US"}}}
	plain := filmSearch{Time: 7, Releases: []found{{GUID: "en", Title: "Film 2000 1080p", Size: gb, Seeders: 1, Indexer: "Public", SiteLanguage: "en-US"}}}
	cases := []struct {
		name   string
		film   dubFilm
		job    string
		search filmSearch
		swept  bool
	}{
		{"never searched", dubFilm{ID: 1, Missing: []string{"spa"}}, "", filmSearch{}, true},
		{"nothing found yet", dubFilm{ID: 1, Missing: []string{"spa"}}, "", plain, true},
		{"has the language", dubFilm{ID: 1, Missing: []string{"cat"}}, "", filmSearch{}, false},
		{"a release names it", dubFilm{ID: 1, Missing: []string{"spa"}}, "", castellano, false},
		{"added, the file has it", dubFilm{ID: 1, Missing: []string{"cat"}}, "done", plain, false},
		{"added, then Radarr upgraded the file", dubFilm{ID: 1, Missing: []string{"spa"}}, "done", castellano, true},
		{"request under way", dubFilm{ID: 1, Missing: []string{"spa"}}, "probing", filmSearch{}, false},
		{"request failed, though a release names it", dubFilm{ID: 1, Missing: []string{"spa"}}, "failed", castellano, true},
		{"request cancelled", dubFilm{ID: 1, Missing: []string{"spa"}}, "cancelled", plain, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d := dubFixture(t)
			d.state.Jobs = map[string]*jobState{"1-cat": {MovieID: 1, Lang: "cat", Status: "done"}}
			if c.job != "" {
				d.state.Jobs["1-spa"] = &jobState{MovieID: 1, Lang: "spa", Status: c.job}
			}
			got := d.toSweep([]dubFilm{c.film}, map[int64]filmSearch{1: c.search})
			if (len(got) == 1) != c.swept {
				t.Errorf("swept %v, want %v", len(got) == 1, c.swept)
			}
		})
	}
}
