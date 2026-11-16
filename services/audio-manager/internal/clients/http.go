// Package clients talks to Radarr, Prowlarr and qBittorrent over their HTTP APIs.
package clients

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// getJSON is a GET with an API key header, decoding the answer into out.
func getJSON(ctx context.Context, client *http.Client, url, apiKey string, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("X-Api-Key", apiKey)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 200))
		return fmt.Errorf("%s: %s %s", redactURL(url), response.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(response.Body).Decode(out); err != nil {
		return fmt.Errorf("%s: %w", redactURL(url), err)
	}
	return nil
}

// redactURL drops the query, which may carry a key, from a URL quoted in an error.
func redactURL(url string) string {
	if i := strings.IndexByte(url, '?'); i >= 0 {
		return url[:i]
	}
	return url
}
