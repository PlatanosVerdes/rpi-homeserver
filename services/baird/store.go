package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// page is a web page that links acestream ids; every id on it is one more source of one channel.
type page struct {
	URL     string    `json:"url"`
	Name    string    `json:"name"`
	Group   string    `json:"group,omitempty"`
	IDs     []string  `json:"ids,omitempty"`
	Checked time.Time `json:"checked,omitempty"`
	Error   string    `json:"error,omitempty"`
}

// choices is everything decided in the UI, the one file worth backing up.
type choices struct {
	Pages      []page   `json:"pages"`
	Picked     []string `json:"picked"`
	Categories []string `json:"categories,omitempty"`
	Countries  []string `json:"countries,omitempty"` // narrows the categories; none means any country
	Excluded   []string `json:"excluded,omitempty"`  // unticked although the rule takes it

	// The lists bifrost reads: everything shows unless hidden here.
	HiddenGroups []string `json:"hiddenGroups,omitempty"`
	Hidden       []string `json:"hidden,omitempty"`
	Shown        []string `json:"shown,omitempty"` // shown although its group is hidden
	KnownGroups  []string `json:"knownGroups,omitempty"`
	NewGroups    []string `json:"newGroups,omitempty"` // appeared upstream after the first read, hidden until switched on
}

type store struct {
	dir string

	mu         sync.Mutex
	choices    choices
	catalogue  []entry
	catalogAt  time.Time
	catalogErr string
	listed     []listed  // the upstream lists, with what they dropped lately
	header     string    // their #EXTM3U line, which names the guide
	upstreamAt time.Time // the last read; a source seen before it is kept, not listed
	listsErr   string
	ids        map[string]string // infohash -> content id; they never change
	m3u        string
	builtAt    time.Time
	channels   int
	sources    int
}

func openStore(dir string) *store {
	s := &store{dir: dir, ids: map[string]string{}}
	readJSON(s.path("choices.json"), &s.choices)
	readJSON(s.path("catalogue.json"), &s.catalogue)
	readJSON(s.path("ids.json"), &s.ids)
	var up struct {
		Header   string   `json:"header"`
		Channels []listed `json:"channels"`
	}
	readJSON(s.path("upstream.json"), &up)
	s.header, s.listed = up.Header, up.Channels
	return s
}

func (s *store) path(name string) string { return filepath.Join(s.dir, name) }

// setPicked ticks or unticks channels; unticking one a picked category covers excludes it.
func (s *store) setPicked(names []string, picked bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	byName := map[string]entry{}
	for _, e := range s.catalogue {
		byName[e.Name] = e
	}
	pickedSet, excluded := toSet(s.choices.Picked), toSet(s.choices.Excluded)
	for _, n := range names {
		pickedSet[n] = picked
		excluded[n] = !picked && s.ruled(byName[n])
	}
	s.choices.Picked, s.choices.Excluded = fromSet(pickedSet), fromSet(excluded)
	s.saveChoices()
}

func (s *store) setCategories(names []string, picked bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.choices.Categories = toggle(s.choices.Categories, names, picked)
	s.saveChoices()
}

func (s *store) setCountries(names []string, picked bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.choices.Countries = toggle(s.choices.Countries, names, picked)
	s.saveChoices()
}

func toggle(values, names []string, on bool) []string {
	set := toSet(values)
	for _, n := range names {
		set[n] = on
	}
	return fromSet(set)
}

func (s *store) addPage(p page) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.choices.Pages {
		if existing.URL == p.URL {
			return fmt.Errorf("already listed")
		}
	}
	s.choices.Pages = append(s.choices.Pages, p)
	s.saveChoices()
	return nil
}

func (s *store) removePage(url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.choices.Pages[:0]
	for _, p := range s.choices.Pages {
		if p.URL != url {
			kept = append(kept, p)
		}
	}
	s.choices.Pages = kept
	s.saveChoices()
}

// pageResult keeps a page's last ids when a fetch fails, so a site down for an hour drops nothing.
func (s *store) pageResult(url string, ids []string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.choices.Pages {
		p := &s.choices.Pages[i]
		if p.URL != url {
			continue
		}
		p.Checked = time.Now()
		if err != nil {
			p.Error = err.Error()
		} else {
			p.IDs, p.Error = ids, ""
		}
	}
	s.saveChoices()
}

func (s *store) setCatalogue(c []entry, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.catalogErr = err.Error()
		return
	}
	s.catalogue, s.catalogAt, s.catalogErr = c, time.Now(), ""
	writeJSON(s.path("catalogue.json"), c)
}

// missingIDs lists the picked infohashes the engine still has to translate.
func (s *store) missingIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, e := range s.pickedEntries() {
		for _, it := range e.Items {
			if s.ids[it.Infohash] == "" {
				out = append(out, it.Infohash)
			}
		}
	}
	return out
}

func (s *store) learnIDs(found map[string]string) {
	if len(found) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range found {
		s.ids[k] = v
	}
	writeJSON(s.path("ids.json"), s.ids)
}

func (s *store) pickedEntries() []entry {
	var out []entry
	for _, e := range s.catalogue {
		if s.included(e) {
			out = append(out, e)
		}
	}
	return out
}

func (s *store) included(e entry) bool {
	if slices.Contains(s.choices.Picked, e.Name) {
		return true
	}
	return s.ruled(e) && !slices.Contains(s.choices.Excluded, e.Name)
}

// allCategories picked takes every category, so "everything from Spain" is one rule.
const allCategories = "*"

// ruled: a live channel of one of the picked categories, and of one of the picked countries when any
// is. Dead ones stay out until the engine sees them live again, so a category does not flood the list.
func (s *store) ruled(e entry) bool {
	anyCategory := slices.Contains(s.choices.Categories, allCategories)
	if !(anyCategory || overlaps(s.choices.Categories, e.Categories)) || !slices.ContainsFunc(e.Items, func(it item) bool { return it.Availability > 0 }) {
		return false
	}
	return len(s.choices.Countries) == 0 || overlaps(s.choices.Countries, countriesOf(e))
}

// countriesOf names a channel with no country "-", so it can be picked like one.
func countriesOf(e entry) []string {
	if len(e.Countries) == 0 {
		return []string{"-"}
	}
	return e.Countries
}

func overlaps(a, b []string) bool {
	for _, v := range b {
		if slices.Contains(a, v) {
			return true
		}
	}
	return false
}

func toSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, v := range values {
		set[v] = true
	}
	return set
}

func fromSet(set map[string]bool) []string {
	var out []string
	for v, on := range set {
		if on {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// render writes baird.m3u: the shown channels of the upstream lists as they wrote them, then the
// pages, then the picked catalogue channels by name.
func (s *store) render() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	if s.header != "" {
		b.WriteString(s.header + "\n")
	} else {
		b.WriteString("#EXTM3U\n")
	}
	channels, sources := 0, 0
	for _, c := range s.listed {
		if !s.shown(c) {
			continue
		}
		channels++
		for _, src := range c.Sources {
			fmt.Fprintf(&b, "%s\nacestream://%s\n", src.Extinf, src.ID)
			sources++
		}
	}
	write := func(name, group, logo, id string) {
		line := "#EXTINF:-1"
		if logo != "" {
			line += ` tvg-logo="` + clean(logo) + `"`
		}
		fmt.Fprintf(&b, "%s group-title=\"%s\",%s\nacestream://%s\n", line, clean(group), clean(name), id)
		sources++
	}
	for _, p := range s.choices.Pages {
		if len(p.IDs) > 0 {
			channels++
		}
		for _, id := range p.IDs {
			write(p.Name, p.Group, "", id)
		}
	}
	entries := s.pickedEntries()
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	for _, e := range entries {
		before := sources
		for _, it := range e.Items {
			if id := s.ids[it.Infohash]; id != "" {
				write(e.Name, groupOf(e), e.Icon, id)
			}
		}
		if sources > before {
			channels++
		}
	}
	s.m3u, s.builtAt, s.channels, s.sources = b.String(), time.Now(), channels, sources
	return writeFile(s.path("baird.m3u"), []byte(s.m3u))
}

func (s *store) knownIcon(u string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.catalogue {
		if u != "" && e.Icon == u {
			return true
		}
	}
	for _, c := range s.listed {
		if u != "" && c.Logo == u {
			return true
		}
	}
	return false
}

func (s *store) playlist() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m3u
}

func (s *store) saveChoices() {
	writeJSON(s.path("choices.json"), s.choices)
}

func groupOf(e entry) string {
	if len(e.Categories) == 0 {
		return "OTROS"
	}
	return strings.ToUpper(e.Categories[0])
}

// clean keeps a value inside its quotes; a comma would also cut the name, read after the last one.
func clean(v string) string {
	return strings.NewReplacer(`"`, "'", ",", " ", "\n", " ", "\r", " ").Replace(strings.TrimSpace(v))
}

func readJSON(path string, v any) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if err := json.Unmarshal(b, v); err != nil {
		log.Printf("baird: %s: %v", path, err)
	}
}

func writeJSON(path string, v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err == nil {
		err = writeFile(path, b)
	}
	if err != nil {
		log.Printf("baird: %s: %v", path, err)
	}
}

func writeFile(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
