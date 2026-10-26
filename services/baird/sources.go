package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

var aceLink = regexp.MustCompile(`(?i)acestream://([0-9a-f]{40})`)

// scrape returns the content ids a page links to, in page order.
func scrape(client *http.Client, pageURL string) ([]string, error) {
	req, err := http.NewRequest("GET", pageURL, nil)
	if err != nil {
		return nil, err
	}
	// Some of these sites answer an empty page to Go's default user agent.
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux aarch64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var ids []string
	seen := map[string]bool{}
	for _, m := range aceLink.FindAllStringSubmatch(string(body), -1) {
		id := strings.ToLower(m[1])
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no acestream links")
	}
	return ids, nil
}

// entry is one channel of the engine's catalogue; its items are the broadcasts carrying it.
type entry struct {
	Name       string   `json:"name"`
	Icon       string   `json:"icon,omitempty"`
	Categories []string `json:"categories,omitempty"`
	Countries  []string `json:"countries,omitempty"`
	Languages  []string `json:"languages,omitempty"`
	Items      []item   `json:"items"`
}

type item struct {
	Infohash     string  `json:"infohash"`
	Bitrate      int     `json:"bitrate,omitempty"`
	Availability float64 `json:"availability"`
}

type engine struct {
	base   string
	client *http.Client
}

const searchPageSize = 1000

// catalogue is what acestreamui.app lists: the engine's own search, every page of it.
func (e *engine) catalogue() ([]entry, error) {
	var out []entry
	for page := 0; page < 50; page++ {
		var body struct {
			Result *struct {
				Total   int `json:"total"`
				Results []struct {
					Name  text `json:"name"`
					Items []struct {
						Infohash     string   `json:"infohash"`
						Bitrate      int      `json:"bitrate"`
						Availability float64  `json:"availability"`
						Categories   []string `json:"categories"`
						Countries    []string `json:"countries"`
						Languages    []string `json:"languages"`
					} `json:"items"`
					Icons []struct {
						URL string `json:"url"`
					} `json:"icons"`
				} `json:"results"`
			} `json:"result"`
			Error string `json:"error"`
		}
		if err := e.get(fmt.Sprintf("/search?page_size=%d&page=%d", searchPageSize, page), &body); err != nil {
			return nil, err
		}
		if body.Result == nil {
			return nil, fmt.Errorf("search: %s", body.Error)
		}
		for _, r := range body.Result.Results {
			en := entry{Name: string(r.Name)}
			if len(r.Icons) > 0 {
				en.Icon = r.Icons[0].URL
			}
			cats, countries, langs := map[string]bool{}, map[string]bool{}, map[string]bool{}
			for _, it := range r.Items {
				en.Items = append(en.Items, item{Infohash: it.Infohash, Bitrate: it.Bitrate, Availability: it.Availability})
				mark(cats, it.Categories)
				mark(countries, it.Countries)
				mark(langs, it.Languages)
			}
			en.Categories, en.Countries, en.Languages = keys(cats), keys(countries), keys(langs)
			out = append(out, en)
		}
		if len(body.Result.Results) < searchPageSize || (page+1)*searchPageSize >= body.Result.Total {
			break
		}
	}
	return out, nil
}

// contentID turns a catalogue infohash into the id bifrost and every player play by.
func (e *engine) contentID(infohash string) (string, error) {
	var body struct {
		Result *struct {
			ContentID string `json:"content_id"`
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := e.get("/server/api?api_version=3&method=get_content_id&infohash="+url.QueryEscape(infohash), &body); err != nil {
		return "", err
	}
	if body.Result == nil || body.Result.ContentID == "" {
		return "", fmt.Errorf("get_content_id: %s", body.Error)
	}
	return body.Result.ContentID, nil
}

func (e *engine) get(path string, v any) error {
	resp, err := e.client.Get(e.base + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// text takes a JSON string or number: the engine sends a channel called "24" as 24.
type text string

func (t *text) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*t = text(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*t = text(n.String())
	return nil
}

func newClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

// mark lowercases: the engine sends both tv and TV, es and ES.
func mark(set map[string]bool, values []string) {
	for _, v := range values {
		if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
			set[v] = true
		}
	}
}

func keys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
