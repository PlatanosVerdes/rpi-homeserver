package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Every indexer Prowlarr has is searched; a release is only tried where taking a few MB and deleting
// it owes nothing: public, or private with a hit & run rule in config/trackers/rules.json that starts
// well above that.

const (
	gb             = 1 << 30
	mb             = 1 << 20
	maxSizeBytes   = 15 * gb
	searchWorkers  = 6
	searchCategory = "2000"
	// A Matroska file lists its tracks in its first few hundred KB; a probe reads what is there from
	// 2 MB on, and gives up on a release that has taken probeBudget without showing them.
	probeHead   = 2 * mb
	probeBudget = 64 * mb
	// A probe stays this far under the progress at which a site's hit & run starts.
	probeMargin = 4
)

var multiAudio = regexp.MustCompile(`(?i)\bmulti|\bdual\b`)

type indexer struct {
	Name     string
	Private  bool
	Language string
}

// flexInt reads Prowlarr's imdbId, a number on most indexers and a string or null on some.
type flexInt int64

func (n *flexInt) UnmarshalJSON(raw []byte) error {
	text := strings.Trim(string(raw), `"`)
	value, err := strconv.ParseInt(strings.TrimLeft(text, "t"), 10, 64)
	if err != nil {
		value = 0
	}
	*n = flexInt(value)
	return nil
}

type release struct {
	GUID         string          `json:"guid"`
	Title        string          `json:"title"`
	Size         int64           `json:"size"`
	Seeders      int             `json:"seeders"`
	ImdbID       flexInt         `json:"imdbId"`
	IndexerID    int             `json:"indexerId"`
	Indexer      string          `json:"indexer"`
	Protocol     string          `json:"protocol"`
	DownloadURL  string          `json:"downloadUrl"`
	IndexerFlags json.RawMessage `json:"indexerFlags"`
}

func (r release) freeleech() bool {
	return strings.Contains(strings.ToLower(string(r.IndexerFlags)), "freeleech")
}

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
func probeAllowed(r release, private bool, rules trackerRules) bool {
	if !private {
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

// candidates are the releases worth probing, those most likely to carry the language first, then
// public before private, freeleech before counted, smaller first. A release without an IMDb id is
// kept: some sites leave it out, and add-audio refuses another film anyway.
func candidates(results []release, imdb string, lang *language, tried map[string]bool, indexers map[int]indexer,
	rules trackerRules, prowlarr string) []candidate {
	number, _ := strconv.ParseInt(strings.TrimLeft(imdb, "t"), 10, 64)
	var fits []candidate
	for _, r := range results {
		site, known := indexers[r.IndexerID]
		imdbID := int64(r.ImdbID)
		if imdbID == 0 {
			imdbID = number
		}
		if !known || imdbID != number || r.Seeders <= 0 || r.Size <= 0 || r.Size > maxSizeBytes || tried[r.GUID] ||
			r.Protocol != "torrent" || r.DownloadURL == "" ||
			(lang.rejectedTitle != nil && lang.rejectedTitle.MatchString(r.Title)) || !probeAllowed(r, site.Private, rules) {
			continue
		}
		locale, _, _ := strings.Cut(site.Language, "-")
		ownSite := locale == lang.Locale
		// MULTi on a French site is French plus the original; it only hints on a site of ours or English.
		multi := multiAudio.MatchString(r.Title) && (ownSite || locale == "en")
		fits = append(fits, candidate{GUID: r.GUID, Title: r.Title, Size: r.Size, Indexer: r.Indexer,
			Private: site.Private, Freeleech: r.freeleech(), URL: withoutKey(r.DownloadURL, prowlarr),
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
	indexers(ctx context.Context) (map[int]indexer, error)
	searchIndexer(ctx context.Context, id int, imdb, title string) ([]release, error)
}

type prowlarr struct {
	base, key string
	service   *dubService
}

func (p prowlarr) indexers(ctx context.Context) (map[int]indexer, error) {
	var list []struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Enable   bool   `json:"enable"`
		Privacy  string `json:"privacy"`
		Language string `json:"language"`
	}
	if err := getJSON(ctx, p.service.http, p.base+"/api/v1/indexer", p.key, &list); err != nil {
		return nil, err
	}
	out := map[int]indexer{}
	for _, i := range list {
		if i.Enable {
			out[i.ID] = indexer{Name: i.Name, Private: i.Privacy != "public", Language: i.Language}
		}
	}
	return out, nil
}

// searchIndexer asks one indexer by IMDb id and by title: NOBS finds nothing by IMDb id, and a title
// match only counts with the same IMDb id.
func (p prowlarr) searchIndexer(ctx context.Context, id int, imdb, title string) ([]release, error) {
	number, _ := strconv.ParseInt(strings.TrimLeft(imdb, "t"), 10, 64)
	var found []release
	seen := map[string]bool{}
	for _, query := range []struct {
		text   string
		strict bool
	}{{"{ImdbId:" + imdb + "}", false}, {title, true}} {
		params := url.Values{"query": {query.text}, "type": {"search"}, "categories": {searchCategory}, "indexerIds": {strconv.Itoa(id)}}
		var results []release
		if err := getJSON(ctx, p.service.searchHTTP, p.base+"/api/v1/search?"+params.Encode(), p.key, &results); err != nil {
			return nil, err
		}
		for _, r := range results {
			if (!query.strict || int64(r.ImdbID) == number) && !seen[r.GUID] {
				seen[r.GUID] = true
				found = append(found, r)
			}
		}
	}
	return found, nil
}

// searchProgress is told about each indexer as it answers.
type searchProgress func(indexer string, done, total, releases int, err error)

// find searches every enabled indexer at once and ranks what they found.
func (d *dubService) find(ctx context.Context, imdb, title, lang string, tried []string, progress searchProgress) ([]candidate, error) {
	known, ok := d.langs.byCode[lang]
	if !ok {
		return nil, fmt.Errorf("unknown language %q", lang)
	}
	indexers, err := d.indexers.indexers(ctx)
	if err != nil {
		return nil, err
	}
	var (
		mu      sync.Mutex
		results []release
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
			found, err := d.indexers.searchIndexer(ctx, id, imdb, title)
			<-slots
			mu.Lock()
			defer mu.Unlock()
			for _, r := range found {
				if !seen[r.GUID] {
					seen[r.GUID] = true
					results = append(results, r)
				}
			}
			done++
			if progress != nil {
				progress(site.Name, done, len(indexers), len(found), err)
			}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rules, err := loadTrackerRules(d.cfg.rulesFile)
	if err != nil {
		return nil, err
	}
	skip := map[string]bool{}
	for _, guid := range tried {
		skip[guid] = true
	}
	return candidates(results, imdb, known, skip, indexers, rules, d.cfg.prowlarrURL), nil
}
