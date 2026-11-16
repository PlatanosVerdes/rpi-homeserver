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
	MovieID    int64       `json:"movie_id"`
	Lang       string      `json:"lang"`
	Title      string      `json:"title"`
	Name       string      `json:"name"`
	IMDb       string      `json:"imdb"`
	Requested  int64       `json:"requested"`
	Tried      []string    `json:"tried"`
	Downloads  int         `json:"downloads"`
	Status     string      `json:"status"` // queued, trying, probing, downloading, done, failed, cancelled
	Reason     string      `json:"reason"`
	Updated    int64       `json:"updated"`
	Candidates []candidate `json:"candidates,omitempty"`
	Current    *torrentRef `json:"current"`
}

type torrentRef struct {
	candidate
	Hash        string  `json:"hash"`
	File        int     `json:"file"`
	Stage       string  `json:"stage"` // probing or downloading
	Since       int64   `json:"since"`
	DeleteBelow float64 `json:"delete_below"`
	Dub         string  `json:"dub,omitempty"`
}

func (j *jobState) finished() bool { return isFinished(j.Status) }

func (j *jobState) clone() *jobState {
	c := *j
	c.Tried = slices.Clone(j.Tried)
	c.Candidates = slices.Clone(j.Candidates)
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
	checks     *checker
	// Replaced in tests: the probe's ffprobe and the run of add-audio.py.
	probeHead   func(path string, size int64) *probeInfo
	runAddAudio func(target, source, lang string) (last string, code int)
	watchFor    time.Duration
	pollEvery   time.Duration

	mu        sync.Mutex
	films     []dubFilm
	filmsTime int64
	filmsErr  string
	state     dubState
	stateErr  string
	refresh   chan struct{}
	wake      chan struct{}
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
	d.checks = newChecker(filepath.Join(cfg.StateDir, "checks.json"), d.check)
	return d
}

// Open loads the languages and what the last run left in cfg.StateDir, failing on a bad file.
func Open(cfg Config) (*Service, error) {
	langs, err := loadLanguages(cfg.LanguagesFile)
	if err != nil {
		return nil, err
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

// load reads what the last run left: the films as Radarr last listed them, the jobs and the checks.
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
	d.checks.load()
}

// saveState is called with d.mu held.
func (d *Service) saveState() {
	if err := store.WriteJSON(d.stateFile(), d.state); err != nil {
		log.Printf("audio-manager: saving %s: %v", d.stateFile(), err)
	}
}

// Start runs the Radarr listing and the job worker in the background.
func (d *Service) Start() {
	go d.filmsLoop()
	go d.worker()
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
	MovieID   int64        `json:"movie_id"`
	Lang      string       `json:"lang"`
	Title     string       `json:"title"`
	Requested int64        `json:"requested"`
	Status    string       `json:"status"`
	Reason    string       `json:"reason"`
	Updated   int64        `json:"updated"`
	Downloads int          `json:"downloads"`
	Tried     int          `json:"tried"`
	Current   *releaseView `json:"current"`
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
		Reason: j.Reason, Updated: j.Updated, Downloads: j.Downloads, Tried: len(j.Tried)}
	if c := j.Current; c != nil {
		v.Current = &releaseView{Title: c.Title, Size: c.Size, Indexer: c.Indexer, Private: c.Private, Stage: c.Stage, Since: c.Since, Dub: c.Dub}
	}
	return v
}

// Payload is the add_audio object of /api/state.
func (d *Service) Payload() map[string]any {
	checks, checksErr := d.checks.snapshot()
	d.mu.Lock()
	defer d.mu.Unlock()
	var jobs []dubJobView
	for _, job := range d.state.Jobs {
		jobs = append(jobs, job.view())
	}
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
	listed := map[string]dubCheck{}
	films := d.films
	if films == nil {
		films = []dubFilm{}
	}
	for _, film := range films {
		for _, lang := range film.Missing {
			if check, ok := checks[dubKey(film.ID, lang)]; ok {
				listed[dubKey(film.ID, lang)] = check
			}
		}
	}
	requests := slices.Clone(d.state.Requests)
	if requests == nil {
		requests = []dubRequest{}
	}
	out := map[string]any{
		"time":          d.filmsTime,
		"languages":     d.langs.names(),
		"films":         films,
		"jobs":          shown,
		"requests":      requests,
		"checks":        listed,
		"charged_bytes": d.state.ChargedBytes,
		"budget_bytes":  d.cfg.Budget,
	}
	var problems []string
	for _, problem := range []string{d.filmsErr, d.stateErr} {
		if problem != "" {
			problems = append(problems, problem)
		}
	}
	if checksErr != nil {
		problems = append(problems, checksErr.Error())
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
