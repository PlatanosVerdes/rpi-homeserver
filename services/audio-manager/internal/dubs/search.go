package dubs

import (
	"audio-manager/internal/clients"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Every indexer Prowlarr has is searched; a release is only tried where taking a few MB and deleting
// it owes nothing: public, or private with a hit & run rule in config/trackers/rules.json that starts
// well above that.

const (
	gb            = 1 << 30
	mb            = 1 << 20
	maxSizeBytes  = 15 * gb
	searchWorkers = 6
	// A Matroska file lists its tracks in its first few hundred KB; a probe reads what is there from
	// 2 MB on, and gives up on a release that has taken probeBudget without showing them.
	probeHead   = 2 * mb
	probeBudget = 64 * mb
	// A probe stays this far under the progress at which a site's hit & run starts.
	probeMargin = 4
)

var multiAudio = regexp.MustCompile(`(?i)\bmulti|\bdual\b`)

// candidate is a release worth probing, in the order to try it.
type candidate struct {
	GUID      string `json:"guid"`
	Title     string `json:"title"`
	Size      int64  `json:"size"`
	Indexer   string `json:"indexer"`
	Private   bool   `json:"private"`
	Freeleech bool   `json:"freeleech"`
	URL       string `json:"url"`
	// What makes it likely to carry the language: its title names it, its site speaks it, or MULTi.
	Named   bool `json:"named"`
	OwnSite bool `json:"own_site"`
	Multi   bool `json:"multi"`
}

func (c candidate) charged() int64 {
	if c.Private && !c.Freeleech {
		return c.Size
	}
	return 0
}

type trackerRule struct {
	ProwlarrIndexer string `json:"prowlarr_indexer"`
	HitAndRun       *struct {
		StartsAtProgress *float64 `json:"starts_at_progress"`
	} `json:"hit_and_run"`
}

// trackerRules keeps the file's order, since the first rule naming an indexer is the one that counts.
type trackerRules []trackerRule

func loadTrackerRules(file string) (trackerRules, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var rules trackerRules
	err = forEachKey(raw, func(_ string, dec *json.Decoder) error {
		var rule trackerRule
		if dec.Decode(&rule) != nil {
			rule = trackerRule{}
		}
		rules = append(rules, rule)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return rules, nil
}

func (rules trackerRules) find(indexer string) *trackerRule {
	for i := range rules {
		if rules[i].ProwlarrIndexer == indexer {
			return &rules[i]
		}
	}
	return nil
}

// startsAt is the progress at which the site's hit & run starts; 1 when it only starts at completion.
func (rules trackerRules) startsAt(indexer string) float64 {
	rule := rules.find(indexer)
	if rule == nil || rule.HitAndRun == nil || rule.HitAndRun.StartsAtProgress == nil {
		return 1
	}
	return *rule.HitAndRun.StartsAtProgress
}

// probeAllowed says whether a few MB of this release can be taken and deleted without owing its site anything.
func probeAllowed(r found, rules trackerRules) bool {
	if !r.Private {
		return true
	}
	if rules.find(r.Indexer) == nil {
		return false
	}
	return float64(probeBudget*probeMargin) < rules.startsAt(r.Indexer)*float64(r.Size)
}

// withoutKey is Prowlarr's download link minus the API key it carries, which goes in a header instead,
// pointed at the Prowlarr this service talks to.
func withoutKey(link, prowlarr string) string {
	parts, err := url.Parse(link)
	if err != nil {
		return ""
	}
	if base, err := url.Parse(prowlarr); err == nil {
		parts.Scheme, parts.Host = base.Scheme, base.Host
	}
	var kept []string
	for _, pair := range strings.Split(parts.RawQuery, "&") {
		key, _, _ := strings.Cut(pair, "=")
		if name, err := url.QueryUnescape(key); pair != "" && (err != nil || !strings.EqualFold(name, "apikey")) {
			kept = append(kept, pair)
		}
	}
	parts.RawQuery = strings.Join(kept, "&")
	return parts.String()
}

// found is a release a search returned that a probe could take whatever the language: a live
// torrent of this film, of a size worth probing. It is what the search cache keeps.
type found struct {
	GUID         string `json:"guid"`
	Title        string `json:"title"`
	Size         int64  `json:"size"`
	Seeders      int    `json:"seeders"`
	Imdb         int64  `json:"imdb"`
	IndexerID    int    `json:"indexer_id"`
	Indexer      string `json:"indexer"`
	Private      bool   `json:"private"`
	SiteLanguage string `json:"site_language"`
	Freeleech    bool   `json:"freeleech"`
	// Never cached: a job searches again for links that still work.
	url string
}

// eligible keeps the releases worth ranking for any language. A release without an IMDb id is kept:
// some sites leave it out, and add-audio refuses another film anyway.
func eligible(results []clients.Release, imdb string, indexers map[int]clients.Indexer, prowlarr string) []found {
	number, _ := strconv.ParseInt(strings.TrimLeft(imdb, "t"), 10, 64)
	out := []found{}
	for _, r := range results {
		site, known := indexers[r.IndexerID]
		imdbID := int64(r.ImdbID)
		if imdbID == 0 {
			imdbID = number
		}
		if !known || imdbID != number || r.Seeders <= 0 || r.Size <= 0 || r.Size > maxSizeBytes ||
			r.Protocol != "torrent" || r.DownloadURL == "" {
			continue
		}
		out = append(out, found{GUID: r.GUID, Title: r.Title, Size: r.Size, Seeders: r.Seeders, Imdb: int64(r.ImdbID),
			IndexerID: r.IndexerID, Indexer: r.Indexer, Private: site.Private, SiteLanguage: site.Language,
			Freeleech: r.Freeleech(), url: withoutKey(r.DownloadURL, prowlarr)})
	}
	return out
}

// candidates are the releases worth probing for a language, those most likely to carry it first,
// then public before private, freeleech before counted, smaller first.
func candidates(releases []found, lang *language, tried map[string]bool, rules trackerRules) []candidate {
	var fits []candidate
	for _, r := range releases {
		if tried[r.GUID] || (lang.rejectedTitle != nil && lang.rejectedTitle.MatchString(r.Title)) ||
			!probeAllowed(r, rules) {
			continue
		}
		locale, _, _ := strings.Cut(r.SiteLanguage, "-")
		ownSite := locale == lang.Locale
		// MULTi on a French site is French plus the original; it only hints on a site of ours or English.
		multi := multiAudio.MatchString(r.Title) && (ownSite || locale == "en")
		fits = append(fits, candidate{GUID: r.GUID, Title: r.Title, Size: r.Size, Indexer: r.Indexer,
			Private: r.Private, Freeleech: r.Freeleech, URL: r.url,
			Named: lang.hint.MatchString(r.Title), OwnSite: ownSite, Multi: multi})
	}
	sort.SliceStable(fits, func(i, j int) bool { return lessCandidate(fits[i], fits[j]) })
	return fits
}

func lessCandidate(a, b candidate) bool {
	// false sorts before true, as in the Python tuple this was.
	for _, pair := range [][2]bool{{!a.Named, !b.Named}, {!a.OwnSite, !b.OwnSite}, {!a.Multi, !b.Multi},
		{a.Private, b.Private}, {!a.Freeleech, !b.Freeleech}} {
		if pair[0] != pair[1] {
			return !pair[0]
		}
	}
	return a.Size < b.Size
}

// indexerClient is Prowlarr, behind an interface so a check can be tested without one.
type indexerClient interface {
	Indexers(ctx context.Context) (map[int]clients.Indexer, error)
	SearchIndexer(ctx context.Context, id int, imdb, title string) ([]clients.Release, error)
}

// searchFilm asks every enabled indexer at once for one film, and keeps what any language could use.
func (d *Service) searchFilm(ctx context.Context, job searchJob, progress searchProgress) (filmSearch, error) {
	indexers, err := d.indexers.Indexers(ctx)
	if err != nil {
		return filmSearch{}, err
	}
	var (
		mu      sync.Mutex
		results []clients.Release
		failed  []string
		seen    = map[string]bool{}
		done    int
		wg      sync.WaitGroup
		slots   = make(chan struct{}, searchWorkers)
	)
	for id, site := range indexers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			answer, err := d.indexers.SearchIndexer(ctx, id, job.imdb, job.title)
			<-slots
			mu.Lock()
			defer mu.Unlock()
			for _, r := range answer {
				if !seen[r.GUID] {
					seen[r.GUID] = true
					results = append(results, r)
				}
			}
			if err != nil {
				failed = append(failed, site.Name+": "+err.Error())
			}
			done++
			if progress != nil {
				progress(site.Name, done, len(indexers), len(answer), err)
			}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return filmSearch{}, err
	}
	slices.Sort(failed)
	return filmSearch{Time: time.Now().Unix(), Indexers: len(indexers), Errors: failed,
		Releases: eligible(results, job.imdb, indexers, d.cfg.ProwlarrURL)}, nil
}

// rank is a search's releases in the order to probe them for a language.
func (d *Service) rank(search filmSearch, lang string, tried []string) ([]candidate, error) {
	known, ok := d.langs.byCode[lang]
	if !ok {
		return nil, fmt.Errorf("unknown language %q", lang)
	}
	rules, err := loadTrackerRules(d.cfg.RulesFile)
	if err != nil {
		return nil, err
	}
	skip := map[string]bool{}
	for _, guid := range tried {
		skip[guid] = true
	}
	return candidates(search.Releases, known, skip, rules), nil
}
