// Package dubs is the Add audio view: it fetches a dub for a library film when the page asks for
// one. One film at a time, oldest first: every indexer is searched, each candidate is probed before
// it is downloaded (the first pieces of its .mkv hold the track list, so a release without the
// language is deleted after a few MB), and one that has it is downloaded and handed to add-audio.py.
// See docs/add-audio.md.
package dubs

import (
	"audio-manager/internal/clients"
	"audio-manager/internal/httpx"
	"audio-manager/internal/store"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	filmsEvery      = 10 * time.Minute
	jobTick         = 30 * time.Second
	dubHTTPTimeout  = 2 * time.Minute
	searchTimeout   = 5 * time.Minute
	maxProbes       = 8
	maxDownloads    = 2
	probeHours      = 6
	stalledHours    = 48
	addAudioTimeout = 4 * time.Hour
)

type Config struct {
	RadarrURL, RadarrKey     string
	ProwlarrURL, ProwlarrKey string
	QbitURL, QbitUser        string
	QbitPassword             string
	RulesFile                string
	LanguagesFile            string
	AddAudio, Python         string
	DataRoot, StateDir       string
	Budget                   int64
	TelegramToken            string
	TelegramChat             string
	// The sweep searches one film every SweepEvery, and a film again once its search is SweepRefresh old,
	// for SweepLanguage only.
	SweepEvery, SweepRefresh time.Duration
	SweepLanguage            string
}

type dubFilm struct {
	ID      int64    `json:"id"`
	Title   string   `json:"title"`
	Year    int      `json:"year"`
	IMDb    string   `json:"imdb"`
	Path    string   `json:"path"`
	Audio   string   `json:"audio"`
	Missing []string `json:"missing"`
}

type dubRequest struct {
	MovieID   int64  `json:"movie_id"`
	Lang      string `json:"lang"`
	Requested int64  `json:"requested"`
	Cancel    bool   `json:"cancel,omitempty"`
}

// jobState is one film and language being fetched; it is kept whole in state.json and never shown
// whole, since its download links lead to private sites.
type jobState struct {
	MovieID    int64         `json:"movie_id"`
	Lang       string        `json:"lang"`
	Title      string        `json:"title"`
	Name       string        `json:"name"`
	IMDb       string        `json:"imdb"`
	Requested  int64         `json:"requested"`
	Tried      []string      `json:"tried"`
	Downloads  int           `json:"downloads"`
	Status     string        `json:"status"` // queued, trying, probing, downloading, done, failed, cancelled
	Reason     string        `json:"reason"`
	Updated    int64         `json:"updated"`
	Candidates []candidate   `json:"candidates,omitempty"`
	Current    *torrentRef   `json:"current"`
	Probes     []probeRecord `json:"probes,omitempty"`
}

// probeRecord is what one probe of a job found, kept so a failed job shows why.
type probeRecord struct {
	Title   string `json:"title"`
	Indexer string `json:"indexer"`
	Private bool   `json:"private"`
	MB      int64  `json:"mb"`
	Outcome string `json:"outcome"`
	Time    int64  `json:"time"`
}

type torrentRef struct {
	candidate
	Hash        string  `json:"hash"`
	File        int     `json:"file"`
	Stage       string  `json:"stage"` // probing or downloading
	Since       int64   `json:"since"`
	DeleteBelow float64 `json:"delete_below"`
	Dub         string  `json:"dub,omitempty"`
	Reused      bool    `json:"reused,omitempty"`
}

// settle is how far the budget must move once a download is over: checkProbe charged its advertised
// size, and what counts is what qBittorrent fetched. A reused source was never charged.
func (t *torrentRef) settle(downloaded int64) int64 {
	if t.Reused || t.charged() == 0 {
		return 0
	}
	return downloaded - t.charged()
}

func (j *jobState) finished() bool { return isFinished(j.Status) }

func (j *jobState) clone() *jobState {
	c := *j
	c.Tried = slices.Clone(j.Tried)
	c.Candidates = slices.Clone(j.Candidates)
	c.Probes = slices.Clone(j.Probes)
	if j.Current != nil {
		current := *j.Current
		c.Current = &current
	}
	return &c
}

func (j *jobState) set(status, reason string) {
	j.Status, j.Reason, j.Updated = status, reason, time.Now().Unix()
}

type dubState struct {
	ChargedBytes int64                `json:"charged_bytes"`
	Jobs         map[string]*jobState `json:"jobs"`
	Requests     []dubRequest         `json:"requests"`
}

type Service struct {
	cfg        Config
	langs      *languages
	http       *http.Client
	searchHTTP *http.Client
	indexers   indexerClient
	searches   *searches
	// Replaced in tests: the probe's ffprobe and the run of add-audio.py.
	probeHead   func(path string, size int64) *probeInfo
	runAddAudio func(target, source, lang string) (last string, code int)
	watchFor    time.Duration
	pollEvery   time.Duration
	// Set while a job searches, so the sweep leaves Prowlarr alone.
	jobSearching atomic.Bool

	mu        sync.Mutex
	films     []dubFilm
	filmsTime int64
	filmsErr  string
	state     dubState
	stateErr  string
	refresh   chan struct{}
	wake      chan struct{}
	lastSweep int64
	counts    countsMemo
}

// countsMemo keeps availableCounts until the films or the searches change.
type countsMemo struct {
	filmsTime  int64
	generation int
	byLang     map[string]int
}

func newService(cfg Config, langs *languages) *Service {
	d := &Service{
		cfg:        cfg,
		langs:      langs,
		http:       &http.Client{Timeout: dubHTTPTimeout},
		searchHTTP: &http.Client{Timeout: searchTimeout},
		watchFor:   300 * time.Second,
		pollEvery:  3 * time.Second,
		state:      dubState{Jobs: map[string]*jobState{}},
		refresh:    make(chan struct{}, 1),
		wake:       make(chan struct{}, 1),
	}
	d.indexers = d.prowlarr()
	d.probeHead = d.ffprobeHead
	d.runAddAudio = d.addAudioScript
	d.searches = newSearches(filepath.Join(cfg.StateDir, "searches.json"), d.searchFilm)
	return d
}

// Open loads the languages and what the last run left in cfg.StateDir, failing on a bad file.
func Open(cfg Config) (*Service, error) {
	langs, err := loadLanguages(cfg.LanguagesFile)
	if err != nil {
		return nil, err
	}
	if _, ok := langs.byCode[cfg.SweepLanguage]; !ok {
		return nil, fmt.Errorf("SWEEP_LANGUAGE %q is not in %s", cfg.SweepLanguage, cfg.LanguagesFile)
	}
	d := newService(cfg, langs)
	d.load()
	return d, nil
}

func (d *Service) radarr() clients.Radarr {
	return clients.Radarr{Base: d.cfg.RadarrURL, Key: d.cfg.RadarrKey, HTTP: d.http}
}

func (d *Service) prowlarr() clients.Prowlarr {
	return clients.Prowlarr{Base: d.cfg.ProwlarrURL, Key: d.cfg.ProwlarrKey, HTTP: d.http, SearchHTTP: d.searchHTTP}
}

func (d *Service) filmsFile() string { return filepath.Join(d.cfg.StateDir, "films.json") }
func (d *Service) stateFile() string { return filepath.Join(d.cfg.StateDir, "state.json") }

// load reads what the last run left: the films as Radarr last listed them, the jobs and the searches.
func (d *Service) load() {
	if err := os.MkdirAll(d.cfg.StateDir, 0o755); err != nil {
		log.Printf("audio-manager: %v", err)
	}
	var films struct {
		Time  int64     `json:"time"`
		Films []dubFilm `json:"films"`
	}
	if err := store.ReadJSON(d.filmsFile(), &films); err != nil {
		log.Printf("audio-manager: %v", err)
	}
	state := dubState{}
	err := store.ReadJSON(d.stateFile(), &state)
	d.mu.Lock()
	d.films, d.filmsTime = films.Films, films.Time
	if err != nil {
		d.stateErr = err.Error()
	}
	if state.Jobs != nil {
		d.state = state
	}
	d.mu.Unlock()
	d.searches.load()
}

// saveState is called with d.mu held.
func (d *Service) saveState() {
	if err := store.WriteJSON(d.stateFile(), d.state); err != nil {
		log.Printf("audio-manager: saving %s: %v", d.stateFile(), err)
	}
}

// Start runs the Radarr listing, the job worker and the sweep in the background.
func (d *Service) Start() {
	go d.filmsLoop()
	go d.worker()
	go d.sweepLoop()
}

func poke(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// filmsLoop asks Radarr again every filmsEvery and whenever a request asks for it.
func (d *Service) filmsLoop() {
	timer := time.NewTimer(0)
	for {
		select {
		case <-timer.C:
		case <-d.refresh:
			timer.Stop()
		}
		d.listFilms()
		timer.Reset(filmsEvery)
	}
}

func (d *Service) listFilms() {
	ctx, cancel := context.WithTimeout(context.Background(), dubHTTPTimeout)
	defer cancel()
	movies, err := d.radarr().Movies(ctx)
	if err != nil {
		log.Printf("audio-manager: Radarr: %v", err)
		d.mu.Lock()
		d.filmsErr = "Radarr did not answer: " + err.Error()
		d.mu.Unlock()
		return
	}
	films := filmsFrom(movies, d.langs)
	now := time.Now().Unix()
	d.mu.Lock()
	d.films, d.filmsTime, d.filmsErr = films, now, ""
	d.mu.Unlock()
	d.searches.keepOnly(films)
	payload := map[string]any{"time": now, "languages": d.langs.names(), "films": films}
	if err := store.WriteJSON(d.filmsFile(), payload); err != nil {
		log.Printf("audio-manager: %v", err)
	}
}

// film is the listed film when it lacks that language; called with d.mu held.
func (d *Service) film(movieID int64, lang string) (dubFilm, bool) {
	for _, f := range d.films {
		if f.ID == movieID && slices.Contains(f.Missing, lang) {
			return f, true
		}
	}
	return dubFilm{}, false
}

func (d *Service) pendingIndex(movieID int64, lang string, cancel bool) int {
	return slices.IndexFunc(d.state.Requests, func(r dubRequest) bool {
		return r.MovieID == movieID && r.Lang == lang && r.Cancel == cancel
	})
}

func (d *Service) activeJob(movieID int64, lang string) bool {
	job, ok := d.state.Jobs[dubKey(movieID, lang)]
	return ok && !job.finished()
}

// requestAdd queues an add for the worker; the error says why it was refused.
func (d *Service) requestAdd(movieID int64, lang string) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.film(movieID, lang); !ok {
		return http.StatusBadRequest, fmt.Errorf("that film is not listed as missing that language")
	}
	if d.activeJob(movieID, lang) {
		return http.StatusConflict, fmt.Errorf("already being added")
	}
	if d.pendingIndex(movieID, lang, false) >= 0 {
		return http.StatusConflict, fmt.Errorf("already requested")
	}
	d.state.Requests = append(d.state.Requests, dubRequest{MovieID: movieID, Lang: lang, Requested: time.Now().Unix()})
	d.saveState()
	poke(d.refresh)
	poke(d.wake)
	return http.StatusOK, nil
}

// requestCancel drops a request not taken yet, or asks the worker to stop the job.
func (d *Service) requestCancel(movieID int64, lang string) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if i := d.pendingIndex(movieID, lang, false); i >= 0 && !d.activeJob(movieID, lang) {
		d.state.Requests = slices.Delete(d.state.Requests, i, i+1)
		d.saveState()
		return http.StatusOK, nil
	}
	if !d.activeJob(movieID, lang) {
		return http.StatusNotFound, fmt.Errorf("nothing to cancel")
	}
	if d.pendingIndex(movieID, lang, true) < 0 {
		d.state.Requests = append(d.state.Requests, dubRequest{MovieID: movieID, Lang: lang, Requested: time.Now().Unix(), Cancel: true})
		d.saveState()
	}
	poke(d.wake)
	return http.StatusOK, nil
}

// cancelWaiting says whether the page asked to cancel this job, so a probe can stop watching.
func (d *Service) cancelWaiting(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.ContainsFunc(d.state.Requests, func(r dubRequest) bool { return r.Cancel && dubKey(r.MovieID, r.Lang) == key })
}

// commit stores the worker's copy of a job, and the money it charged.
func (d *Service) commit(key string, job *jobState, charged int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.state.Jobs[key] = job.clone()
	d.state.ChargedBytes += charged
	d.saveState()
}

func (d *Service) worker() {
	ticker := time.NewTicker(jobTick)
	defer ticker.Stop()
	for {
		d.tick()
		select {
		case <-ticker.C:
		case <-d.wake:
		}
	}
}

// tick is one pass: take the page's requests, then advance the oldest active job as far as it goes.
func (d *Service) tick() {
	p := &pass{d: d, ctx: context.Background()}
	defer p.notify()
	d.mu.Lock()
	requests := slices.Clone(d.state.Requests)
	// Until Radarr has listed the films once, a request cannot be matched to one, and would be lost.
	if d.filmsTime == 0 {
		requests = nil
	}
	d.mu.Unlock()
	for _, request := range requests {
		if err := p.take(request); err != nil {
			log.Printf("audio-manager: request %s: %v", dubKey(request.MovieID, request.Lang), err)
			break
		}
	}

	d.mu.Lock()
	var active []*jobState
	for _, job := range d.state.Jobs {
		if !job.finished() {
			active = append(active, job)
		}
	}
	sort.Slice(active, func(i, j int) bool {
		if active[i].Requested != active[j].Requested {
			return active[i].Requested < active[j].Requested
		}
		return dubKey(active[i].MovieID, active[i].Lang) < dubKey(active[j].MovieID, active[j].Lang)
	})
	var job *jobState
	if len(active) > 0 {
		job = active[0].clone()
	}
	d.mu.Unlock()
	if job != nil {
		p.advance(job)
	}
}

// view is what the page sees of a job.
type dubJobView struct {
	MovieID   int64         `json:"movie_id"`
	Lang      string        `json:"lang"`
	Title     string        `json:"title"`
	Requested int64         `json:"requested"`
	Status    string        `json:"status"`
	Reason    string        `json:"reason"`
	Updated   int64         `json:"updated"`
	Downloads int           `json:"downloads"`
	Tried     int           `json:"tried"`
	Current   *releaseView  `json:"current"`
	Probes    []probeRecord `json:"probes,omitempty"`
}

type releaseView struct {
	Title   string `json:"title"`
	Size    int64  `json:"size"`
	Indexer string `json:"indexer"`
	Private bool   `json:"private"`
	Stage   string `json:"stage"`
	Since   int64  `json:"since"`
	Dub     string `json:"dub"`
}

func (j *jobState) view() dubJobView {
	v := dubJobView{MovieID: j.MovieID, Lang: j.Lang, Title: j.Title, Requested: j.Requested, Status: j.Status,
		Reason: j.Reason, Updated: j.Updated, Downloads: j.Downloads, Tried: len(j.Tried), Probes: j.Probes}
	if c := j.Current; c != nil {
		v.Current = &releaseView{Title: c.Title, Size: c.Size, Indexer: c.Indexer, Private: c.Private, Stage: c.Stage, Since: c.Since, Dub: c.Dub}
	}
	return v
}

// Payload is the add_audio object of /api/state, with each film's availability in lang.
func (d *Service) Payload(lang string) map[string]any {
	done, generation, searchesErr := d.searches.snapshot()
	live := d.searches.underway()
	d.mu.Lock()
	var jobs []dubJobView
	for _, job := range d.state.Jobs {
		jobs = append(jobs, job.view())
	}
	films, filmsTime, lastSweep, memo := d.films, d.filmsTime, d.lastSweep, d.counts
	if films == nil {
		films = []dubFilm{}
	}
	requests := slices.Clone(d.state.Requests)
	if requests == nil {
		requests = []dubRequest{}
	}
	out := map[string]any{
		"time":          filmsTime,
		"languages":     d.langs.names(),
		"films":         films,
		"requests":      requests,
		"searching":     live,
		"charged_bytes": d.state.ChargedBytes,
		"budget_bytes":  d.cfg.Budget,
	}
	problems := slices.DeleteFunc([]string{d.filmsErr, d.stateErr}, func(p string) bool { return p == "" })
	d.mu.Unlock()

	sort.Slice(jobs, func(i, j int) bool {
		ai, aj := !isFinished(jobs[i].Status), !isFinished(jobs[j].Status)
		if ai != aj {
			return ai
		}
		return jobs[i].Updated > jobs[j].Updated
	})
	shown, finished := []dubJobView{}, 0
	for _, job := range jobs {
		if isFinished(job.Status) {
			if finished++; finished > httpx.HistoryShown {
				continue
			}
		}
		shown = append(shown, job)
	}
	out["jobs"] = shown
	out["sweep"] = d.sweepStatus(films, done, lastSweep)

	available := map[int64]availability{}
	rules, err := loadTrackerRules(d.cfg.RulesFile)
	if err != nil {
		problems = append(problems, err.Error())
	} else {
		if known, ok := d.langs.byCode[lang]; ok {
			for _, f := range films {
				if search, ok := done[f.ID]; ok && slices.Contains(f.Missing, lang) {
					available[f.ID] = availabilityOf(search, known, rules)
				}
			}
		}
		if memo.byLang == nil || memo.filmsTime != filmsTime || memo.generation != generation {
			memo = countsMemo{filmsTime: filmsTime, generation: generation, byLang: availableCounts(films, done, d.langs, rules)}
			d.mu.Lock()
			d.counts = memo
			d.mu.Unlock()
		}
		out["available_counts"] = memo.byLang
	}
	out["lang"] = lang
	out["availability"] = available
	if searchesErr != nil {
		problems = append(problems, searchesErr.Error())
	}
	if len(problems) > 0 {
		out["error"] = strings.Join(problems, "; ")
	}
	return out
}

func isFinished(status string) bool {
	return status == "done" || status == "failed" || status == "cancelled"
}

func dubKey(movieID int64, lang string) string { return fmt.Sprintf("%d-%s", movieID, lang) }
