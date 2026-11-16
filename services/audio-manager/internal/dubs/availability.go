package dubs

import "slices"

// What a film's cached search means for one language: how many releases could be probed, how many
// name the language, how many come from a site in it, and the three most likely.

type checkRelease struct {
	Title   string `json:"title"`
	Size    int64  `json:"size"`
	Indexer string `json:"indexer"`
	Private bool   `json:"private"`
}

type availability struct {
	Time     int64          `json:"time"`
	Releases int            `json:"releases"`
	Named    int            `json:"named"`
	OwnSite  int            `json:"own_site"`
	Best     []checkRelease `json:"best"`
	Indexers int            `json:"indexers_total"`
	Failing  int            `json:"indexers_failed,omitempty"`
	// A later search that failed; the counts are still the last good one's, if any.
	Failed int64  `json:"failed,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (a availability) available() bool { return a.Named > 0 || a.OwnSite > 0 }

func summarise(found []candidate) availability {
	result := availability{Releases: len(found), Best: []checkRelease{}}
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

func availabilityOf(search filmSearch, lang *language, rules trackerRules) availability {
	a := summarise(candidates(search.Releases, lang, nil, rules))
	a.Time, a.Indexers, a.Failing = search.Time, search.Indexers, len(search.Errors)
	if search.Failed > search.Time {
		a.Failed, a.Error = search.Failed, search.Error
	}
	return a
}

// availableCounts is, per language, how many of the films lacking it have a release naming it or from a
// site in it.
func availableCounts(films []dubFilm, done map[int64]filmSearch, langs *languages, rules trackerRules) map[string]int {
	out := make(map[string]int, len(langs.order))
	for _, code := range langs.order {
		lang := langs.byCode[code]
		for _, f := range films {
			search, ok := done[f.ID]
			if !ok || search.Time == 0 || !slices.Contains(f.Missing, code) {
				continue
			}
			if availabilityOf(search, lang, rules).available() {
				out[code]++
			}
		}
	}
	return out
}
