package dubs

import (
	"audio-manager/internal/clients"
	"audio-manager/internal/httpx"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func testLanguages(t *testing.T) *languages {
	t.Helper()
	langs, err := loadLanguages("testdata/languages.json")
	if err != nil {
		t.Fatal(err)
	}
	return langs
}

func testRules(t *testing.T) trackerRules {
	t.Helper()
	rules, err := loadTrackerRules("testdata/rules.json")
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

// dubFixture is an Add audio service that lists three films and knows two jobs.
func dubFixture(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	cfg := Config{StateDir: filepath.Join(dir, "state", "add-audio"), DataRoot: filepath.Join(dir, "data"), RulesFile: "testdata/rules.json",
		ProwlarrURL: "http://127.0.0.1:9696", Budget: 100 * gb,
		SweepEvery: 5 * time.Minute, SweepRefresh: 7 * 24 * time.Hour, SweepLanguage: "spa"}
	os.MkdirAll(filepath.Join(cfg.DataRoot, "films", "A"), 0o755)
	os.WriteFile(filepath.Join(cfg.DataRoot, "films", "A", "a.mkv"), []byte("x"), 0o644)
	d := newService(cfg, testLanguages(t))
	d.load()
	d.films = []dubFilm{
		{ID: 11, Title: "The Others", Year: 2001, IMDb: "tt0230600", Audio: "eng", Missing: []string{"spa", "cat"}},
		{ID: 12, Title: "Heat", Year: 1995, IMDb: "tt0113277", Audio: "eng", Missing: []string{"spa", "cat"}},
		{ID: 13, Title: "Rec", Year: 2007, IMDb: "tt1038988", Audio: "spa", Missing: []string{"cat"}},
	}
	d.filmsTime = 1
	d.state.ChargedBytes = 5
	d.state.Jobs = map[string]*jobState{
		"12-spa": {MovieID: 12, Lang: "spa", Title: "Heat (1995)", Requested: 1, Status: "downloading", Updated: 3, Downloads: 1,
			Tried: []string{"https://indexer/x?passkey=secret"},
			Current: &torrentRef{candidate: candidate{Title: "Heat.1995.SPANiSH", Size: 9, Indexer: "NOBS", Private: true,
				URL: "https://indexer/dl?passkey=secret"}, Hash: "abc", Stage: "downloading", Since: 2}},
		"12-cat": {MovieID: 12, Lang: "cat", Title: "Heat (1995)", Status: "failed", Reason: "no dub found", Updated: 4},
	}
	return d
}

// addAudioPayload is the add_audio object of /api/state, decoded as the page decodes it.
func addAudioPayload(t *testing.T, d *Service) (map[string]any, string) {
	t.Helper()
	w := httptest.NewRecorder()
	httpx.WriteJSON(w, http.StatusOK, d.Payload("spa"))
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body, w.Body.String()
}

func dubPost(handler http.HandlerFunc, movieID, lang string) (int, map[string]any) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(url.Values{"movie_id": {movieID}, "lang": {lang}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler(w, r)
	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	return w.Code, body
}

func TestLanguagesKeepTheFileOrder(t *testing.T) {
	t.Parallel()
	langs := testLanguages(t)
	if !slices.Equal(langs.order, []string{"spa", "cat", "eng", "fra"}) {
		t.Errorf("order %v", langs.order)
	}
	cat := langs.byCode["cat"]
	for title, want := range map[string]bool{"Film 2000 Català 1080p": true, "Film.2000.CATALA": true, "Cat People": false, "Catalana": false} {
		if got := cat.hint.MatchString(title); got != want {
			t.Errorf("%q names Català: %v, want %v", title, got, want)
		}
	}
}

func TestFilmsListMkvFilmsWithTheLanguagesTheyLack(t *testing.T) {
	t.Parallel()
	movie := func(id int64, audio, path string) clients.Movie {
		m := clients.Movie{ID: id, Title: fmt.Sprintf("Film %d", id), Year: 2000, ImdbID: "tt0000001", HasFile: true}
		m.MovieFile = &clients.MovieFile{Path: path, MediaInfo: &clients.MediaInfo{AudioLanguages: audio}}
		return m
	}
	mkv := "/data/films/A (2000)/a.mkv"
	noFile := movie(5, "eng", mkv)
	noFile.HasFile = false
	listed := filmsFrom([]clients.Movie{movie(1, "eng", mkv), movie(2, "eng/spa", mkv), movie(3, "Spanish / English / English", mkv),
		movie(4, "eng", "/data/films/B/b.mp4"), noFile}, testLanguages(t))
	var got []string
	for _, f := range listed {
		got = append(got, fmt.Sprintf("%d spa:%v eng:%v %s", f.ID, slices.Contains(f.Missing, "spa"), slices.Contains(f.Missing, "eng"), f.Audio))
	}
	want := []string{"1 spa:true eng:false eng", "2 spa:false eng:false eng/spa", "3 spa:false eng:false Spanish/English"}
	if !slices.Equal(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestHostPath(t *testing.T) {
	t.Parallel()
	d := &Service{cfg: Config{DataRoot: "/mnt/data"}}
	for in, want := range map[string]string{"/data/films/A/a.mkv": "/mnt/data/films/A/a.mkv", "/data": "/mnt/data", "/database/x": "/database/x"} {
		if got := d.hostPath(in); got != want {
			t.Errorf("hostPath(%q) = %q, want %q", in, got, want)
		}
	}
}

var (
	testIndexerNames = map[int]string{1: "Public", 2: "NOBS", 3: "TorrentLeech", 4: "Unknown private", 5: "C411"}
	testIndexers     = map[int]clients.Indexer{
		1: {Name: "Public", Private: false, Language: "en-US"},
		2: {Name: "NOBS", Private: true, Language: "es-ES"},
		3: {Name: "TorrentLeech", Private: true, Language: "en-US"},
		4: {Name: "Unknown private", Private: true, Language: "en-US"},
		5: {Name: "C411", Private: true, Language: "fr-FR"},
	}
)

type releaseOption func(*clients.Release)

func onIndexer(id int) releaseOption {
	return func(r *clients.Release) { r.IndexerID, r.Indexer = id, testIndexerNames[id] }
}
func withSeeders(n int) releaseOption { return func(r *clients.Release) { r.Seeders = n } }
func withImdb(n int64) releaseOption {
	return func(r *clients.Release) { r.ImdbID = clients.FlexInt(n) }
}
func withTitle(title string) releaseOption { return func(r *clients.Release) { r.Title = title } }
func freeleech() releaseOption {
	return func(r *clients.Release) { r.IndexerFlags = json.RawMessage(`["freeleech"]`) }
}

func testRelease(guid string, sizeGB float64, options ...releaseOption) clients.Release {
	r := clients.Release{GUID: guid, Size: int64(sizeGB * gb), Seeders: 5, ImdbID: 1, Title: "Film (2000)", Protocol: "torrent",
		IndexerFlags: json.RawMessage(`[]`)}
	onIndexer(1)(&r)
	for _, option := range options {
		option(&r)
	}
	r.DownloadURL = fmt.Sprintf("http://prowlarr:9696/%d/download?apikey=secret&link=abc&file=%s", r.IndexerID, guid)
	return r
}

func pick(t *testing.T, results []clients.Release, tried ...string) []string {
	t.Helper()
	skip := map[string]bool{}
	for _, guid := range tried {
		skip[guid] = true
	}
	var guids []string
	releases := eligible(results, "tt0000001", testIndexers, "http://127.0.0.1:9696")
	for _, c := range candidates(releases, testLanguages(t).byCode["spa"], skip, testRules(t)) {
		guids = append(guids, c.GUID)
	}
	return guids
}

func TestCandidates(t *testing.T) {
	t.Parallel()
	tiny := float64(probeBudget*probeMargin*2) / gb
	cases := []struct {
		name    string
		results []clients.Release
		tried   []string
		want    []string
	}{
		{"public first, then private freeleech, then private, smaller first",
			[]clients.Release{testRelease("tl", 2, onIndexer(3)), testRelease("pub-big", 9), testRelease("pub", 3),
				testRelease("tl-fl", 6, onIndexer(3), freeleech())},
			nil, []string{"pub", "pub-big", "tl-fl", "tl"}},
		{"likely carriers of the language first; MULTi on a French site does not count",
			[]clients.Release{testRelease("plain", 1), testRelease("multi", 2, withTitle("Film 2000 MULTi 1080p")),
				testRelease("french-multi", 0.5, onIndexer(5), withTitle("Film 2000 MULTI VFF")),
				testRelease("spanish-site", 3, onIndexer(2)), testRelease("named", 4, onIndexer(3), withTitle("Film 2000 Castellano"))},
			nil, []string{"named", "spanish-site", "multi", "plain", "french-multi"}},
		{"a site in a third language goes last, even when its title names the language",
			[]clients.Release{testRelease("foreign", 1, onIndexer(5), withTitle("Film 2000 Castellano MULTi")),
				testRelease("private", 6, onIndexer(3)), testRelease("public", 9)},
			nil, []string{"public", "private", "foreign"}},
		{"a release without an IMDb id is kept", []clients.Release{testRelease("a", 2, withImdb(0))}, nil, []string{"a"}},
		{"dead, tried, oversized, Latin or another film are skipped",
			[]clients.Release{testRelease("a", 2, withSeeders(0)), testRelease("b", 2), testRelease("c", 40), testRelease("d", 2, withImdb(2)),
				testRelease("e", 2, withTitle("Film LATINO")), testRelease("f", 1)},
			[]string{"f"}, []string{"b"}},
		// TorrentLeech's obligation starts at completion; an unlisted private site is never touched.
		{"probes only where deleting a few MB owes nothing",
			[]clients.Release{testRelease("tl", 2, onIndexer(3)), testRelease("x", 2, onIndexer(4))}, nil, []string{"tl"}},
		{"not where the hit & run starts too early", []clients.Release{testRelease("nobs", tiny*0.9, onIndexer(2))}, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := pick(t, c.results, c.tried...); !slices.Equal(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestCandidatesNeverKeepProwlarrsAPIKey(t *testing.T) {
	t.Parallel()
	releases := eligible([]clients.Release{testRelease("a", 2)}, "tt0000001", testIndexers, "http://127.0.0.1:9696")
	found := candidates(releases, testLanguages(t).byCode["spa"], nil, testRules(t))
	if link := found[0].URL; strings.Contains(link, "secret") || !strings.HasPrefix(link, "http://127.0.0.1:9696/1/download?link=abc") {
		t.Errorf("url %s", link)
	}
}

func TestOnlyACountedDownloadCostsBudget(t *testing.T) {
	t.Parallel()
	for c, want := range map[candidate]int64{
		{Size: 2 * gb, Private: true}:                  2 * gb,
		{Size: 2 * gb, Private: true, Freeleech: true}: 0,
		{Size: 2 * gb}: 0,
	} {
		if got := c.charged(); got != want {
			t.Errorf("%+v charged %d, want %d", c, got, want)
		}
	}
}

func TestReadyBytesCountsTheDownloadedRunFromTheFilesStart(t *testing.T) {
	t.Parallel()
	cases := []struct {
		states            []int
		first             int
		offset, pieceSize int64
		want              int64
	}{
		{[]int{2, 2, 2, 0, 2}, 0, 0, mb, 3 * mb},
		{[]int{2, 2, 2, 0}, 1, mb / 2, mb, 2*mb + mb/2},
		{[]int{0, 2}, 0, 0, mb, 0},
	}
	for _, c := range cases {
		if got := readyBytes(c.states, c.first, c.offset, c.pieceSize); got != c.want {
			t.Errorf("readyBytes(%v, %d, %d) = %d, want %d", c.states, c.first, c.offset, got, c.want)
		}
	}
}

func TestInfohash(t *testing.T) {
	t.Parallel()
	info := "d6:lengthi5e4:name5:a.mkv12:piece lengthi16384e6:pieces20:" + strings.Repeat("x", 20) + "e"
	sum := sha1.Sum([]byte(info))
	if got, err := infohash([]byte("d8:announce10:http://a/b4:info" + info + "e")); err != nil || got != hex.EncodeToString(sum[:]) {
		t.Errorf("infohash %s %v", got, err)
	}
	for _, bad := range []string{"<html>login</html>", "d8:announce", "d3:fooi1ee"} {
		if _, err := infohash([]byte(bad)); err == nil {
			t.Errorf("%q taken for a torrent", bad)
		}
	}
}

func TestPickDub(t *testing.T) {
	t.Parallel()
	stream := func(index int, codec string, channels int, lang, title string) probeStream {
		s := probeStream{Index: index, CodecType: "audio", CodecName: codec, Channels: channels}
		s.Tags.Language, s.Tags.Title = lang, title
		return s
	}
	spa := testLanguages(t).byCode["spa"]
	cases := []struct {
		name    string
		streams []probeStream
		want    int
	}{
		{"none in the language", []probeStream{stream(1, "ac3", 6, "eng", "")}, -1},
		{"Latin American is another variant", []probeStream{stream(1, "ac3", 6, "spa", "Latino")}, -1},
		{"not a commentary", []probeStream{stream(1, "ac3", 6, "spa", "Commentary"), stream(2, "aac", 2, "spa", "")}, 2},
		{"the preferred title first", []probeStream{stream(1, "ac3", 6, "spa", ""), stream(2, "aac", 2, "spa", "Castellano")}, 2},
		{"then AC3, then channels", []probeStream{stream(1, "eac3", 8, "spa", ""), stream(2, "ac3", 2, "spa", ""), stream(3, "ac3", 6, "spa", "")}, 3},
		{"the first of equals", []probeStream{stream(1, "ac3", 6, "spa", ""), stream(2, "ac3", 6, "es", "")}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := pickDub(&probeInfo{Streams: c.streams}, spa)
			if (got == nil && c.want != -1) || (got != nil && got.Index != c.want) {
				t.Errorf("picked %+v, want #%d", got, c.want)
			}
		})
	}
}

func TestAddAudioState(t *testing.T) {
	t.Parallel()
	d := dubFixture(t)
	got, raw := addAudioPayload(t, d)
	if strings.Contains(raw, "secret") || strings.Contains(raw, `"hash"`) {
		t.Errorf("the state leaked a link or a hash: %s", raw)
	}
	films, jobs := got["films"].([]any), got["jobs"].([]any)
	first := jobs[0].(map[string]any)
	current, _ := first["current"].(map[string]any)
	if len(films) != 3 || len(jobs) != 2 || first["status"] != "downloading" || first["tried"] != 1.0 ||
		current["title"] != "Heat.1995.SPANiSH" || current["indexer"] != "NOBS" {
		t.Errorf("films, jobs or the active job first: %s", raw)
	}
	if got["languages"].(map[string]any)["spa"] != "Castellano" || got["charged_bytes"] != 5.0 {
		t.Errorf("languages or budget: %s", raw)
	}
	if _, has := got["error"]; has {
		t.Errorf("unexpected error: %v", got["error"])
	}
}

func TestAddAudioRequest(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, movieID, lang string
		want                int
	}{
		{"unknown language", "11", "fre", http.StatusBadRequest},
		{"film not missing it", "13", "spa", http.StatusBadRequest},
		{"film not listed", "99", "spa", http.StatusBadRequest},
		{"not a number", "x", "spa", http.StatusBadRequest},
		{"active job", "12", "spa", http.StatusConflict},
		{"after a failed job", "12", "cat", http.StatusOK},
		{"new", "11", "spa", http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d := dubFixture(t)
			if code, body := dubPost(d.ServeAdd, c.movieID, c.lang); code != c.want {
				t.Fatalf("code %d, want %d: %v", code, c.want, body)
			}
			if queued := len(d.state.Requests) == 1; queued != (c.want == http.StatusOK) {
				t.Errorf("requests: %+v", d.state.Requests)
			}
		})
	}

	d := dubFixture(t)
	dubPost(d.ServeAdd, "11", "spa")
	if code, _ := dubPost(d.ServeAdd, "11", "spa"); code != http.StatusConflict {
		t.Errorf("duplicate request: %d", code)
	}
	got, raw := addAudioPayload(t, d)
	if requests := got["requests"].([]any); len(requests) != 1 || requests[0].(map[string]any)["movie_id"] != 11.0 {
		t.Errorf("pending request not in the state: %s", raw)
	}
}

func TestAddAudioCancel(t *testing.T) {
	t.Parallel()
	d := dubFixture(t)
	if code, _ := dubPost(d.ServeCancel, "11", "spa"); code != http.StatusNotFound {
		t.Errorf("cancel with nothing under way: %d", code)
	}
	if code, _ := dubPost(d.ServeCancel, "12", "cat"); code != http.StatusNotFound {
		t.Errorf("cancel of a finished job: %d", code)
	}
	if code, body := dubPost(d.ServeCancel, "12", "spa"); code != http.StatusOK {
		t.Fatalf("cancel of an active job: %d %v", code, body)
	}
	if want := (dubRequest{MovieID: 12, Lang: "spa", Cancel: true}); len(d.state.Requests) != 1 ||
		d.state.Requests[0].Cancel != want.Cancel || d.state.Requests[0].MovieID != want.MovieID {
		t.Errorf("cancel request: %+v", d.state.Requests)
	}
	dubPost(d.ServeAdd, "11", "spa")
	if code, _ := dubPost(d.ServeCancel, "11", "spa"); code != http.StatusOK || len(d.state.Requests) != 1 {
		t.Errorf("a pending request not dropped: %d %+v", code, d.state.Requests)
	}
}

func TestTakeRequests(t *testing.T) {
	t.Parallel()
	d := dubFixture(t)
	d.state.Jobs = map[string]*jobState{}
	d.state.Requests = []dubRequest{{MovieID: 11, Lang: "spa"}, {MovieID: 13, Lang: "spa"}}
	p := &pass{d: d, ctx: context.Background()}
	for _, request := range slices.Clone(d.state.Requests) {
		if err := p.take(request); err != nil {
			t.Fatal(err)
		}
	}
	if len(d.state.Jobs) != 1 || d.state.Jobs["11-spa"].Status != "queued" || d.state.Jobs["11-spa"].Title != "The Others (2001)" ||
		len(d.state.Requests) != 0 {
		t.Fatalf("a request for a film lacking the language becomes a job: %+v %+v", d.state.Jobs, d.state.Requests)
	}
	if err := p.take(dubRequest{MovieID: 11, Lang: "spa", Cancel: true}); err != nil {
		t.Fatal(err)
	}
	if d.state.Jobs["11-spa"].Status != "cancelled" {
		t.Errorf("cancel ends a job that has no torrent yet: %+v", d.state.Jobs["11-spa"])
	}
}

func TestDubStatePersists(t *testing.T) {
	t.Parallel()
	d := dubFixture(t)
	d.mu.Lock()
	d.saveState()
	d.mu.Unlock()
	again := newService(d.cfg, d.langs)
	again.load()
	if again.state.ChargedBytes != 5 || len(again.state.Jobs) != 2 || again.state.Jobs["12-spa"].Current.URL != "https://indexer/dl?passkey=secret" {
		t.Errorf("round trip: %+v", again.state)
	}
}

// fakeIndexers answers a check or a search without Prowlarr.
type fakeIndexers struct {
	sites   map[int]clients.Indexer
	results map[int][]clients.Release
	fail    map[int]error
}

func (f fakeIndexers) Indexers(context.Context) (map[int]clients.Indexer, error) { return f.sites, nil }

func (f fakeIndexers) SearchIndexer(_ context.Context, id int, _, _ string) ([]clients.Release, error) {
	return f.results[id], f.fail[id]
}

func TestSearchReportsEachIndexerThenWhatItFound(t *testing.T) {
	t.Parallel()
	d := dubFixture(t)
	d.indexers = fakeIndexers{
		sites: map[int]clients.Indexer{1: testIndexers[1], 2: testIndexers[2], 4: testIndexers[4]},
		results: map[int][]clients.Release{1: {testRelease("plain", 1), testRelease("named", 2, withTitle("Film 2000 Castellano"))},
			2: {testRelease("site", 3, onIndexer(2))}},
		fail: map[int]error{4: errors.New("timed out")},
	}
	var mu sync.Mutex
	var done []int
	errorsSeen := 0
	search, err := d.searchFilm(context.Background(), searchJob{id: 11, imdb: "tt0000001", title: "Film"},
		func(_ string, n, total, _ int, err error) {
			mu.Lock()
			defer mu.Unlock()
			done = append(done, n)
			if total != 3 {
				t.Errorf("total %d", total)
			}
			if err != nil {
				errorsSeen++
			}
		})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(done)
	if !slices.Equal(done, []int{1, 2, 3}) || errorsSeen != 1 {
		t.Errorf("progress %v, %d errors", done, errorsSeen)
	}
	if search.Indexers != 3 || len(search.Errors) != 1 || !strings.HasPrefix(search.Errors[0], "Unknown private: timed out") {
		t.Errorf("indexers %d, errors %q", search.Indexers, search.Errors)
	}
	result := availabilityOf(search, testLanguages(t).byCode["spa"], testRules(t))
	if result.Releases != 3 || result.Named != 1 || result.OwnSite != 1 || result.Best[0].Title != "Film 2000 Castellano" {
		t.Errorf("result %+v", result)
	}
}

// torrentFile is a .torrent holding one file, and its infohash.
func torrentFile(name string) ([]byte, string) {
	info := fmt.Sprintf("d6:lengthi%de4:name%d:%s12:piece lengthi%de6:pieces20:%se", 10*mb, len(name), name, mb, strings.Repeat("x", 20))
	sum := sha1.Sum([]byte(info))
	return []byte("d8:announce10:http://a/b4:info" + info + "e"), hex.EncodeToString(sum[:])
}

// fakeQbit is the part of qBittorrent's API a job uses.
type fakeQbit struct {
	mu       sync.Mutex
	torrents map[string]*fakeTorrent
	deleted  []string
	released []string
	kept     []string
	added    int
}

type fakeTorrent struct {
	name     string
	title    string
	category string
	started  bool
	progress float64
}

func (f *fakeQbit) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r.ParseMultipartForm(1 << 20)
	hash := r.FormValue("hash") + r.FormValue("hashes")
	t := f.torrents[hash]
	switch strings.TrimPrefix(r.URL.Path, "/api/v2/") {
	case "auth/login":
		w.WriteHeader(http.StatusNoContent)
	case "torrents/info":
		if category := r.FormValue("category"); category != "" {
			list := []map[string]any{}
			for h, each := range f.torrents {
				if each.category == category {
					list = append(list, map[string]any{"hash": h, "name": each.title, "category": each.category, "progress": each.progress})
				}
			}
			json.NewEncoder(w).Encode(list)
			return
		}
		if t == nil {
			w.Write([]byte("[]"))
			return
		}
		downloaded := 0
		if t.started {
			downloaded = 10 * mb
		}
		json.NewEncoder(w).Encode([]map[string]any{{"downloaded": downloaded, "progress": t.progress, "category": t.category,
			"save_path": "/data/downloads", "download_path": ""}})
	case "torrents/categories":
		w.Write([]byte("{}"))
	case "torrents/add":
		file, _, err := r.FormFile("torrents")
		if err != nil || r.FormValue("tags") != "audio-source,keep" || r.FormValue("stopped") != "true" {
			w.Write([]byte("Fails."))
			return
		}
		raw, _ := io.ReadAll(file)
		added, _ := infohash(raw)
		// Names under ten bytes: "4:name9:named.mkv12:piece length...".
		name := strings.Split(string(raw), "4:name")[1][2:]
		name = name[:strings.Index(name, "12:piece")]
		f.torrents[added] = &fakeTorrent{name: name, category: "audio-source"}
		f.added++
	case "torrents/files":
		json.NewEncoder(w).Encode([]map[string]any{{"name": "Release/" + t.name, "size": 10 * mb, "piece_range": []int{0, 9}}})
	case "torrents/start":
		if t == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		t.started = true
	case "torrents/pieceStates":
		states := make([]int, 10)
		for i := range states {
			if t.started {
				states[i] = 2
			}
		}
		json.NewEncoder(w).Encode(states)
	case "torrents/properties":
		w.Write([]byte(`{"piece_size": 1048576}`))
	case "torrents/delete":
		delete(f.torrents, hash)
		f.deleted = append(f.deleted, hash)
	case "torrents/removeTags":
		f.released = append(f.released, hash)
	case "torrents/addTags":
		f.kept = append(f.kept, hash)
	}
}

func TestJobProbesDownloadsAndAddsTheDub(t *testing.T) {
	t.Parallel()
	d := dubFixture(t)
	d.state = dubState{Jobs: map[string]*jobState{}}
	d.watchFor, d.pollEvery = 2*time.Second, time.Millisecond

	named, namedHash := torrentFile("named.mkv")
	multi, multiHash := torrentFile("multi.mkv")
	var badKey bool
	prowlarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "key" || r.URL.Query().Has("apikey") {
			badKey = true
		}
		switch r.URL.Query().Get("file") {
		case "named":
			w.Write(named)
		case "multi":
			w.Write(multi)
		}
	}))
	defer prowlarrServer.Close()
	d.cfg.ProwlarrURL, d.cfg.ProwlarrKey = prowlarrServer.URL, "key"
	d.indexers = fakeIndexers{sites: map[int]clients.Indexer{1: testIndexers[1]}, results: map[int][]clients.Release{1: {
		testRelease("named", 2, withTitle("The Others 2001 Castellano"), withImdb(230600)),
		testRelease("multi", 3, withTitle("The Others 2001 MULTi"), withImdb(230600)),
	}}}

	qb := &fakeQbit{torrents: map[string]*fakeTorrent{}}
	qbitServer := httptest.NewServer(qb)
	defer qbitServer.Close()
	d.cfg.QbitURL = qbitServer.URL

	radarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id": 11, "movieFile": {"path": "/data/films/A/a.mkv"}}`))
	}))
	defer radarrServer.Close()
	d.cfg.RadarrURL = radarrServer.URL

	// The title that names Castellano carries only English; the MULTi one has it.
	d.probeHead = func(path string, size int64) *probeInfo {
		lang := "eng"
		if strings.HasSuffix(path, "multi.mkv") {
			lang = "spa"
		}
		s := probeStream{Index: 1, CodecType: "audio", CodecName: "ac3", Channels: 6}
		s.Tags.Language = lang
		if lang == "spa" {
			s.Tags.Title = "Castellano"
		}
		return &probeInfo{Streams: []probeStream{s}}
	}
	var added []string
	d.runAddAudio = func(target, source, lang string) (string, int) {
		added = append(added, target, source, lang)
		return "added a spa track", 0
	}

	d.state.Requests = []dubRequest{{MovieID: 11, Lang: "spa", Requested: 1}}
	d.tick()
	job := d.state.Jobs["11-spa"]
	if job == nil || job.Status != "downloading" || job.Current.GUID != "multi" || job.Downloads != 1 || len(job.Tried) != 2 {
		t.Fatalf("after the first pass: %+v", job)
	}
	if !slices.Equal(qb.deleted, []string{namedHash}) || badKey {
		t.Errorf("the probe without the language is deleted: %v; key in the URL: %v", qb.deleted, badKey)
	}
	if !strings.Contains(job.Current.Dub, "spa") {
		t.Errorf("dub %q", job.Current.Dub)
	}
	var probes []string
	for _, probe := range job.Probes {
		probes = append(probes, fmt.Sprintf("%s %s %d", probe.Title, probe.Outcome, probe.MB))
	}
	if want := []string{"The Others 2001 Castellano no Castellano: eng 10", "The Others 2001 MULTi has Castellano 10"}; !slices.Equal(probes, want) {
		t.Errorf("probes %q, want %q", probes, want)
	}
	if _, raw := addAudioPayload(t, d); !strings.Contains(raw, `"outcome":"no Castellano: eng"`) {
		t.Errorf("the page does not get the probes: %s", raw)
	}

	d.tick()
	if d.state.Jobs["11-spa"].Status != "downloading" {
		t.Fatalf("an unfinished download waits: %+v", d.state.Jobs["11-spa"])
	}
	qb.torrents[multiHash].progress = 1
	d.tick()
	job = d.state.Jobs["11-spa"]
	if job.Status != "done" || job.Reason != "added a spa track" || job.Current != nil {
		t.Fatalf("after the download: %+v", job)
	}
	want := []string{filepath.Join(d.cfg.DataRoot, "films/A/a.mkv"), filepath.Join(d.cfg.DataRoot, "downloads/Release/multi.mkv"), "spa"}
	if !slices.Equal(added, want) {
		t.Errorf("add-audio ran with %q, want %q", added, want)
	}
	if !slices.Equal(qb.released, []string{multiHash}) || d.state.ChargedBytes != 0 {
		t.Errorf("the download goes to qbit-manage: released %v, charged %d", qb.released, d.state.ChargedBytes)
	}
}

func TestJobReusesASourceDownloadedBefore(t *testing.T) {
	t.Parallel()
	d := dubFixture(t)
	d.state = dubState{Jobs: map[string]*jobState{}}
	named, _ := torrentFile("named.mkv")
	multi, multiHash := torrentFile("multi.mkv")
	prowlarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("file") == "named" {
			w.Write(named)
		} else {
			w.Write(multi)
		}
	}))
	defer prowlarrServer.Close()
	d.cfg.ProwlarrURL, d.cfg.ProwlarrKey = prowlarrServer.URL, "key"
	d.indexers = fakeIndexers{sites: map[int]clients.Indexer{1: testIndexers[1]}, results: map[int][]clients.Release{1: {
		testRelease("named", 2, withTitle("The Others 2001 Castellano"), withImdb(230600)),
		testRelease("multi", 3, withTitle("The Others 2001 MULTi"), withImdb(230600)),
	}}}
	qb := &fakeQbit{torrents: map[string]*fakeTorrent{
		multiHash: {name: "multi.mkv", title: "The Others 2001 MULTi.mkv", category: "audio-source", progress: 1},
	}}
	qbitServer := httptest.NewServer(qb)
	defer qbitServer.Close()
	d.cfg.QbitURL = qbitServer.URL
	radarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id": 11, "movieFile": {"path": "/data/films/A/a.mkv"}}`))
	}))
	defer radarrServer.Close()
	d.cfg.RadarrURL = radarrServer.URL
	d.probeHead = func(path string, size int64) *probeInfo {
		s := probeStream{Index: 1, CodecType: "audio", CodecName: "ac3", Channels: 6}
		s.Tags.Language, s.Tags.Title = "spa", "Castellano"
		return &probeInfo{Streams: []probeStream{s}}
	}
	var sources []string
	d.runAddAudio = func(target, source, lang string) (string, int) {
		sources = append(sources, source)
		return "added a spa track", 0
	}

	d.state.Requests = []dubRequest{{MovieID: 11, Lang: "spa", Requested: 1}}
	d.tick()
	job := d.state.Jobs["11-spa"]
	if job.Status != "done" || job.Downloads != 0 || len(job.Tried) != 1 || qb.added != 0 {
		t.Fatalf("the downloaded source goes first and nothing is fetched: %+v, %d added", job, qb.added)
	}
	if !slices.Equal(sources, []string{filepath.Join(d.cfg.DataRoot, "downloads/Release/multi.mkv")}) {
		t.Errorf("add-audio ran on %q", sources)
	}
	if !slices.Equal(qb.kept, []string{multiHash}) || !slices.Equal(qb.released, []string{multiHash}) {
		t.Errorf("held while add-audio runs, then released: kept %v, released %v", qb.kept, qb.released)
	}
	if job.Probes[0].Outcome != "downloaded already, has Castellano" {
		t.Errorf("probes %+v", job.Probes)
	}
}

func TestLargestMKV(t *testing.T) {
	t.Parallel()
	file := func(name string, size int64) clients.QbitFile { return clients.QbitFile{Name: name, Size: size} }
	cases := []struct {
		name  string
		files []clients.QbitFile
		want  int
	}{
		{"the film", []clients.QbitFile{file("Sample/s.mkv", 30*mb), file("film.mkv", 9*gb), file("film.nfo", 1)}, 1},
		{"packed, only the sample is .mkv", []clients.QbitFile{file("Sample/s.mkv", 30*mb), file("film.r00", 450*mb),
			file("film.r01", 450*mb), file("film.rar", 450*mb)}, -1},
		{"no .mkv", []clients.QbitFile{file("film.mp4", gb)}, -1},
	}
	for _, c := range cases {
		if got := largestMKV(c.files); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
}

func TestSettleCountsWhatWasFetched(t *testing.T) {
	t.Parallel()
	counted := torrentRef{candidate: candidate{Size: 9 * gb, Private: true}}
	reused := counted
	reused.Reused = true
	free := torrentRef{candidate: candidate{Size: 9 * gb, Private: true, Freeleech: true}}
	if got := counted.settle(30 * mb); got != 30*mb-9*gb {
		t.Errorf("counted: %d", got)
	}
	if reused.settle(30*mb) != 0 || free.settle(30*mb) != 0 {
		t.Error("a reused or freeleech source charges nothing")
	}
}

func TestJobStopsAtTheBudget(t *testing.T) {
	t.Parallel()
	d := dubFixture(t)
	d.cfg.Budget = gb
	job := &jobState{MovieID: 11, Lang: "spa", Title: "The Others (2001)", Status: "trying", Tried: []string{},
		Candidates: []candidate{{GUID: "tl", Title: "The Others", Size: 2 * gb, Private: true}}}
	p := &pass{d: d, ctx: context.Background()}
	p.advance(job)
	if job.Status != "failed" || job.Reason != "none of the 1 releases probed carried it" {
		t.Errorf("a counted download over the budget is skipped: %+v", job)
	}
}

func TestDubChecks(t *testing.T) {
	t.Parallel()
	langs := testLanguages(t)
	spa, cat := langs.byCode["spa"], langs.byCode["cat"]
	track := func(title string) *probeStream {
		s := &probeStream{CodecType: "audio"}
		s.Tags.Language, s.Tags.Title = "spa", title
		return s
	}
	cases := []struct {
		name    string
		dub     *probeStream
		release candidate
		lang    *language
		want    bool
	}{
		{"the track says Castellano", track("Castellano AC3 5.1"), candidate{Title: "Film MULTi"}, spa, true},
		{"the release says Castellano", track(""), candidate{Title: "Film 2000 Castellano 1080p"}, spa, true},
		{"from a Spanish site", track(""), candidate{Title: "Film MULTi", OwnSite: true}, spa, true},
		{"spa and nothing else", track(""), candidate{Title: "Film MULTi"}, spa, false},
		{"Spanish could be Latino", track("Spanish"), candidate{Title: "Film 2000 SPANiSH"}, spa, false},
		{"a language without variants", track(""), candidate{Title: "Film MULTi"}, cat, true},
	}
	for _, c := range cases {
		if got := confirmed(c.dub, c.release, c.lang); got != c.want {
			t.Errorf("%s: confirmed %v, want %v", c.name, got, c.want)
		}
	}

	film := 127 * 60.0
	for _, c := range []struct {
		name   string
		source float64
		want   bool
	}{
		{"longer credits", film + 83, true},
		{"PAL speed-up", film * 23.976 / 25, true},
		{"extended edition", film + 15*60, false},
		{"a sample", 60, false},
		{"length unknown", 0, true},
	} {
		if got := sameCut(c.source, film); got != c.want {
			t.Errorf("%s: same cut %v, want %v", c.name, got, c.want)
		}
	}
}

func TestReadTracksTrustsTheFileOverRadarr(t *testing.T) {
	t.Parallel()
	d := dubFixture(t)
	dir := filepath.Join(d.cfg.DataRoot, "films", "A")
	stream := func(lang, title string) probeStream {
		s := probeStream{CodecType: "audio", CodecName: "ac3"}
		s.Tags.Language, s.Tags.Title = lang, title
		return s
	}
	probes := 0
	d.probeFile = func(path string) *probeInfo {
		probes++
		info := &probeInfo{Streams: []probeStream{stream("eng", ""), stream("spa", "Español Latino")}}
		info.Format.Duration = "7641.7"
		return info
	}
	films := []dubFilm{{ID: 1, Path: "/data/films/A/a.mkv", Audio: "English/Spanish", Missing: []string{"cat"}}}
	d.readTracks(films, nil)
	if !slices.Contains(films[0].Missing, "spa") || slices.Contains(films[0].Missing, "eng") || films[0].File.Length != 7641.7 {
		t.Errorf("a Latino track is not Castellano: %+v", films[0])
	}
	again := []dubFilm{{ID: 1, Path: "/data/films/A/a.mkv", Missing: []string{}}}
	d.readTracks(again, films)
	if probes != 1 || !slices.Contains(again[0].Missing, "spa") {
		t.Errorf("an unchanged file is not read again: %d probes, %+v", probes, again[0])
	}
	os.WriteFile(filepath.Join(dir, "a.mkv"), []byte("upgraded"), 0o644)
	d.readTracks(again, films)
	if probes != 2 {
		t.Errorf("a changed file is read again: %d probes", probes)
	}
	gone := []dubFilm{{ID: 2, Path: "/data/films/B/b.mkv", Missing: []string{"spa"}}}
	d.readTracks(gone, nil)
	if gone[0].File != nil || !slices.Equal(gone[0].Missing, []string{"spa"}) {
		t.Errorf("a file that cannot be read keeps Radarr's word: %+v", gone[0])
	}
}

func TestAddAudioWaitsForALowLoad(t *testing.T) {
	t.Parallel()
	d := dubFixture(t)
	d.cfg.MaxLoad = 4
	load := 9.0
	d.loadAverage = func() (float64, error) { return load, nil }
	ran := 0
	d.runAddAudio = func(target, source, lang string) (string, int) { ran++; return "added a spa track", 0 }
	qb := &fakeQbit{torrents: map[string]*fakeTorrent{"abc": {name: "x.mkv", category: "audio-source", progress: 1}}}
	qbitServer := httptest.NewServer(qb)
	defer qbitServer.Close()
	d.cfg.QbitURL = qbitServer.URL
	radarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id": 11, "movieFile": {"path": "/data/films/A/a.mkv"}}`))
	}))
	defer radarrServer.Close()
	d.cfg.RadarrURL = radarrServer.URL
	job := &jobState{MovieID: 11, Lang: "spa", Title: "The Others (2001)", Status: "downloading",
		Current: &torrentRef{candidate: candidate{Title: "x"}, Hash: "abc", Stage: "downloading"}}
	p := &pass{d: d, ctx: context.Background()}
	p.advance(job)
	if ran != 0 || job.Status != "downloading" || !strings.Contains(job.Reason, "load 9.0") {
		t.Fatalf("waits while the load is high: ran %d, %+v", ran, job)
	}
	load = 1
	p.advance(job)
	if ran != 1 || job.Status != "done" {
		t.Errorf("runs once it is low: ran %d, %+v", ran, job)
	}
}

func TestJobTriesAnUnconfirmedDubLast(t *testing.T) {
	t.Parallel()
	d := dubFixture(t)
	d.state = dubState{Jobs: map[string]*jobState{}}
	d.watchFor, d.pollEvery = 2*time.Second, time.Millisecond
	multi, multiHash := torrentFile("multi.mkv")
	prowlarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(multi) }))
	defer prowlarrServer.Close()
	d.cfg.ProwlarrURL, d.cfg.ProwlarrKey = prowlarrServer.URL, "key"
	d.indexers = fakeIndexers{sites: map[int]clients.Indexer{1: testIndexers[1]}, results: map[int][]clients.Release{1: {
		testRelease("multi", 3, withTitle("The Others 2001 MULTi"), withImdb(230600)),
	}}}
	qb := &fakeQbit{torrents: map[string]*fakeTorrent{}}
	qbitServer := httptest.NewServer(qb)
	defer qbitServer.Close()
	d.cfg.QbitURL = qbitServer.URL
	radarrServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id": 11, "movieFile": {"path": "/data/films/A/a.mkv"}}`))
	}))
	defer radarrServer.Close()
	d.cfg.RadarrURL = radarrServer.URL
	d.probeHead = func(path string, size int64) *probeInfo {
		s := probeStream{Index: 1, CodecType: "audio", CodecName: "ac3", Channels: 6}
		s.Tags.Language = "spa"
		return &probeInfo{Streams: []probeStream{s}}
	}
	d.runAddAudio = func(target, source, lang string) (string, int) { return "added a spa track", 0 }

	d.state.Requests = []dubRequest{{MovieID: 11, Lang: "spa", Requested: 1}}
	d.tick()
	job := d.state.Jobs["11-spa"]
	if job.Status != "downloading" || !job.Unconfirmed || len(job.Probes) != 2 || !strings.Contains(job.Probes[0].Outcome, "not confirmed") {
		t.Fatalf("probed, kept for last, then taken when nothing else had it: %+v", job)
	}
	qb.torrents[multiHash].progress = 1
	d.tick()
	if job = d.state.Jobs["11-spa"]; job.Status != "done" || !strings.Contains(job.Reason, "check it") {
		t.Errorf("the result says to check it: %+v", job)
	}
}
