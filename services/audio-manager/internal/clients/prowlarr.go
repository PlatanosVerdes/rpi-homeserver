package clients

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const searchCategory = "2000"

type Prowlarr struct {
	Base, Key string
	HTTP      *http.Client
	// A search waits on the slowest indexer, so it gets a client with a longer timeout.
	SearchHTTP *http.Client
}

type Indexer struct {
	Name     string
	Private  bool
	Language string
}

// FlexInt reads Prowlarr's imdbId, a number on most indexers and a string or null on some.
type FlexInt int64

func (n *FlexInt) UnmarshalJSON(raw []byte) error {
	text := strings.Trim(string(raw), `"`)
	value, err := strconv.ParseInt(strings.TrimLeft(text, "t"), 10, 64)
	if err != nil {
		value = 0
	}
	*n = FlexInt(value)
	return nil
}

type Release struct {
	GUID         string          `json:"guid"`
	Title        string          `json:"title"`
	Size         int64           `json:"size"`
	Seeders      int             `json:"seeders"`
	ImdbID       FlexInt         `json:"imdbId"`
	IndexerID    int             `json:"indexerId"`
	Indexer      string          `json:"indexer"`
	Protocol     string          `json:"protocol"`
	DownloadURL  string          `json:"downloadUrl"`
	IndexerFlags json.RawMessage `json:"indexerFlags"`
}

func (r Release) Freeleech() bool {
	return strings.Contains(strings.ToLower(string(r.IndexerFlags)), "freeleech")
}

// Indexers are the enabled indexers, by id.
func (p Prowlarr) Indexers(ctx context.Context) (map[int]Indexer, error) {
	var list []struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Enable   bool   `json:"enable"`
		Privacy  string `json:"privacy"`
		Language string `json:"language"`
	}
	if err := getJSON(ctx, p.HTTP, p.Base+"/api/v1/indexer", p.Key, &list); err != nil {
		return nil, err
	}
	out := map[int]Indexer{}
	for _, i := range list {
		if i.Enable {
			out[i.ID] = Indexer{Name: i.Name, Private: i.Privacy != "public", Language: i.Language}
		}
	}
	return out, nil
}

// SearchIndexer asks one indexer by IMDb id and by title: NOBS finds nothing by IMDb id, and a title
// match only counts with the same IMDb id.
func (p Prowlarr) SearchIndexer(ctx context.Context, id int, imdb, title string) ([]Release, error) {
	number, _ := strconv.ParseInt(strings.TrimLeft(imdb, "t"), 10, 64)
	var found []Release
	seen := map[string]bool{}
	for _, query := range []struct {
		text   string
		strict bool
	}{{"{ImdbId:" + imdb + "}", false}, {title, true}} {
		params := url.Values{"query": {query.text}, "type": {"search"}, "categories": {searchCategory}, "indexerIds": {strconv.Itoa(id)}}
		var results []Release
		if err := getJSON(ctx, p.SearchHTTP, p.Base+"/api/v1/search?"+params.Encode(), p.Key, &results); err != nil {
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

// DownloadTorrent fetches the .torrent through Prowlarr, its API key in a header.
func (p Prowlarr) DownloadTorrent(ctx context.Context, link string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-Api-Key", p.Key)
	response, err := p.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the .torrent: %s", response.Status)
	}
	return io.ReadAll(io.LimitReader(response.Body, 32<<20))
}
