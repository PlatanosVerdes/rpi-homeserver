package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// listed is a channel of the upstream lists, with every source it has carried lately.
type listed struct {
	Name    string     `json:"name"`
	Group   string     `json:"group,omitempty"`
	Logo    string     `json:"logo,omitempty"`
	Sources []upstream `json:"sources"`
}

// upstream is one source line, kept as written so bifrost gets the list's own tags.
type upstream struct {
	ID     string    `json:"id"`
	Extinf string    `json:"extinf"`
	Seen   time.Time `json:"seen"` // last time a list carried it
}

// "Channel 1080p **" is a source of the channel "Channel 1080p", as bifrost groups them.
var sourceMarker = regexp.MustCompile(`\s*\*+$`)

type upstreamRead struct {
	channels []listed // in list order, one entry per source line
	header   string   // the first list's #EXTM3U line, which carries its guide
}

// readUpstream reads every list of the file, each by the first route that answers with channels,
// else its last copy. A gateway that answers 200 with no channels counts as down.
func readUpstream(client *http.Client, file, cacheDir string) (upstreamRead, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return upstreamRead{}, err
	}
	var entries []struct {
		URL    string   `json:"url"`
		Backup []string `json:"backup"`
	}
	if err := json.Unmarshal(b, &entries); err != nil {
		return upstreamRead{}, fmt.Errorf("%s: %v", file, err)
	}
	var out upstreamRead
	for _, e := range entries {
		chans, header := readList(client, append([]string{e.URL}, e.Backup...), cacheDir)
		out.channels = append(out.channels, chans...)
		if out.header == "" {
			out.header = header
		}
	}
	return out, nil
}

func readList(client *http.Client, routes []string, cacheDir string) ([]listed, string) {
	cache := filepath.Join(cacheDir, hashName(routes[0]))
	for _, route := range routes {
		resp, err := client.Get(route)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			continue
		}
		if chans, header := parseList(data); len(chans) > 0 {
			if os.MkdirAll(cacheDir, 0o755) == nil {
				writeFile(cache, data)
			}
			return chans, header
		}
	}
	data, _ := os.ReadFile(cache)
	return parseList(data)
}

func hashName(route string) string {
	sum := sha256.Sum256([]byte(route))
	return fmt.Sprintf("%x.m3u", sum[:6])
}

func parseList(data []byte) ([]listed, string) {
	var out []listed
	var header, extinf string
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "#EXTM3U"):
			if header == "" {
				header = line
			}
		case strings.HasPrefix(line, "#EXTINF"):
			extinf = line
		case strings.HasPrefix(line, "acestream://") && extinf != "":
			out = append(out, listed{Name: channelOf(extinf), Group: attr(extinf, "group-title"), Logo: attr(extinf, "tvg-logo"),
				Sources: []upstream{{ID: strings.TrimPrefix(line, "acestream://"), Extinf: extinf}}})
			extinf = ""
		}
	}
	return out, header
}

func channelOf(extinf string) string {
	name := extinf
	if i := strings.LastIndex(extinf, ","); i >= 0 {
		name = extinf[i+1:]
	}
	return strings.TrimSpace(sourceMarker.ReplaceAllString(strings.TrimSpace(name), ""))
}

func attr(line, name string) string {
	i := strings.Index(line, name+`="`)
	if i < 0 {
		return ""
	}
	rest := line[i+len(name)+2:]
	if j := strings.Index(rest, `"`); j >= 0 {
		return rest[:j]
	}
	return ""
}

// keeping says what is kept of what the upstream dropped, which it does to sources that still play.
// The player tries three or four sources per open at most, so more than that is dead weight.
type keeping struct {
	For time.Duration // how long after the upstream dropped it
	Max int           // kept sources per channel, on top of the listed ones
}

// remember folds a read into what the lists carried before. Kept sources that the player saw play
// go first, then the most recently dropped; one it saw fail goes at once. A channel the upstream
// dropped whole stays only while one of its sources has played.
func remember(before []listed, read []listed, now time.Time, k keeping, outcomes map[string]string) []listed {
	index := map[string]int{}
	out := make([]listed, 0, len(before))
	for _, c := range before {
		index[c.Name] = len(out)
		out = append(out, listed{Name: c.Name, Group: c.Group, Logo: c.Logo, Sources: c.Sources})
	}
	carried := map[string]bool{}
	for _, r := range read {
		i, ok := index[r.Name]
		if !ok {
			index[r.Name] = len(out)
			out = append(out, listed{Name: r.Name})
			i = len(out) - 1
		}
		c := &out[i]
		c.Group, c.Logo = r.Group, r.Logo
		for _, src := range r.Sources {
			src.Seen = now
			carried[src.ID] = true
			if j := slices.IndexFunc(c.Sources, func(s upstream) bool { return s.ID == src.ID }); j >= 0 {
				c.Sources[j] = src
			} else {
				c.Sources = append(c.Sources, src)
			}
		}
	}
	played := func(s upstream) bool { return outcomes[s.ID] == "played" || outcomes[s.ID] == "probed" }
	result := out[:0]
	for _, c := range out {
		var listedNow, kept []upstream
		for _, s := range c.Sources {
			switch {
			case carried[s.ID]:
				listedNow = append(listedNow, s)
			case outcomes[s.ID] != "failed" && now.Sub(s.Seen) < k.For:
				kept = append(kept, s)
			}
		}
		if len(listedNow) == 0 {
			kept = slices.DeleteFunc(kept, func(s upstream) bool { return !played(s) })
		}
		slices.SortStableFunc(kept, func(a, b upstream) int {
			if played(a) != played(b) {
				if played(a) {
					return -1
				}
				return 1
			}
			return b.Seen.Compare(a.Seen)
		})
		c.Sources = append(listedNow, kept[:min(len(kept), k.Max)]...)
		if len(c.Sources) > 0 {
			result = append(result, c)
		}
	}
	return result
}

// outcomes asks the player how each source did: played, probed or failed. Only a hint: when it does
// not answer, nothing is known and the time limit is all there is.
func outcomes(client *http.Client, playerURL string) map[string]string {
	if playerURL == "" {
		return nil
	}
	resp, err := client.Get(strings.TrimRight(playerURL, "/") + "/api/channels")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var body struct {
		Channels []struct {
			Sources []struct {
				ID      string `json:"id"`
				Outcome string `json:"outcome"`
			} `json:"sources"`
		} `json:"channels"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&body) != nil {
		return nil
	}
	out := map[string]string{}
	for _, c := range body.Channels {
		for _, s := range c.Sources {
			if s.Outcome != "" {
				out[s.ID] = s.Outcome
			}
		}
	}
	return out
}

func (s *store) setUpstream(read upstreamRead, err error, seen map[string]string, k keeping) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.listsErr = err.Error()
		return
	}
	s.listsErr = ""
	if read.header != "" {
		s.header = read.header
	}
	s.upstreamAt = time.Now()
	s.listed = remember(s.listed, read.channels, s.upstreamAt, k, seen)
	s.holdNewGroups()
	writeJSON(s.path("upstream.json"), struct {
		Header   string   `json:"header"`
		Channels []listed `json:"channels"`
	}{s.header, s.listed})
}

// holdNewGroups hides a group the upstream starts carrying, so nobody else's additions reach the
// list unseen. The first read takes every group as it is.
func (s *store) holdNewGroups() {
	known := toSet(s.choices.KnownGroups)
	var fresh []string
	for _, c := range s.listed {
		if !known[c.Group] {
			known[c.Group] = true
			fresh = append(fresh, c.Group)
		}
	}
	if len(fresh) == 0 {
		return
	}
	if len(s.choices.KnownGroups) > 0 {
		s.choices.HiddenGroups = toggle(s.choices.HiddenGroups, fresh, true)
		s.choices.NewGroups = toggle(s.choices.NewGroups, fresh, true)
	}
	s.choices.KnownGroups = toggle(s.choices.KnownGroups, fresh, true)
	s.saveChoices()
}

// shown: a hidden group hides its channels, a channel can be hidden or shown on its own on top.
func (s *store) shown(c listed) bool {
	if slices.Contains(s.choices.Shown, c.Name) {
		return true
	}
	return !slices.Contains(s.choices.HiddenGroups, c.Group) && !slices.Contains(s.choices.Hidden, c.Name)
}

func (s *store) setListGroups(names []string, shown bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.choices.HiddenGroups = toggle(s.choices.HiddenGroups, names, !shown)
	s.choices.NewGroups = toggle(s.choices.NewGroups, names, false)
	s.saveChoices()
}

func (s *store) setListChannels(names []string, shown bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.choices.Hidden = toggle(s.choices.Hidden, names, !shown)
	s.choices.Shown = toggle(s.choices.Shown, names, false)
	if shown {
		var hiddenByGroup []string
		for _, c := range s.listed {
			if slices.Contains(names, c.Name) && slices.Contains(s.choices.HiddenGroups, c.Group) {
				hiddenByGroup = append(hiddenByGroup, c.Name)
			}
		}
		s.choices.Shown = toggle(s.choices.Shown, hiddenByGroup, true)
	}
	s.saveChoices()
}
