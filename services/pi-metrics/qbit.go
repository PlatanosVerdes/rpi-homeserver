package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
)

// Reading qBittorrent used to go through `docker exec qbittorrent curl`, which needed no
// credentials because the WebUI trusts its own localhost. From a container it arrives over the
// Docker bridge, which is not in the AuthSubnetWhitelist, so it logs in like any other client and
// keeps the cookie for the process's life.
var (
	qbitURL    = env("QBIT_URL", "http://qbittorrent:8080")
	qbitMu     sync.Mutex
	qbitCookie string
)

func qbitLogin() error {
	body := url.Values{
		"username": {os.Getenv("QBIT_USER")},
		"password": {os.Getenv("QBIT_PASSWORD")},
	}
	request, err := http.NewRequest("POST", qbitURL+"/api/v2/auth/login", strings.NewReader(body.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Referer", qbitURL)
	resp, err := client.Do(request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	for _, cookie := range resp.Cookies() {
		// qBittorrent 5 names it QBT_SID_<port>, 4.x named it SID: take whatever it sent.
		if cookie.Value != "" {
			qbitCookie = cookie.Name + "=" + cookie.Value
			return nil
		}
	}
	return fmt.Errorf("qBittorrent refused the login")
}

func qbitCall(path string) ([]byte, error) {
	qbitMu.Lock()
	defer qbitMu.Unlock()
	for attempt := 0; attempt < 2; attempt++ {
		request, err := http.NewRequest("GET", qbitURL+"/api/v2/"+path, nil)
		if err != nil {
			return nil, err
		}
		if qbitCookie != "" {
			request.Header.Set("Cookie", qbitCookie)
		}
		resp, err := client.Do(request)
		if err != nil {
			return nil, err
		}
		payload, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusForbidden && attempt == 0 {
			if err := qbitLogin(); err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode >= 300 {
			return nil, fmt.Errorf("qbittorrent %s: %s", path, resp.Status)
		}
		return payload, readErr
	}
	return nil, fmt.Errorf("qbittorrent %s: still forbidden after a login", path)
}

// qbitTorrents returns nil when qBittorrent could not be asked, which is not the same as "no
// torrents": orphan detection has to tell those apart or a qBittorrent that is merely down turns
// every download into an orphan.
func qbitTorrents() ([]map[string]any, error) {
	payload, err := qbitCall("torrents/info")
	if err != nil {
		return nil, err
	}
	var items []map[string]any
	if err := json.Unmarshal(payload, &items); err != nil {
		return nil, err
	}
	return items, nil
}
