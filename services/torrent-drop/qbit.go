package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
)

// The WebUI trusts its own localhost, but a call from another container arrives over the Docker
// bridge, which is not in its AuthSubnetWhitelist, so this logs in like any other client and keeps
// the cookie for the process's life.
var (
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

func qbitGet(path string, query url.Values) (string, error) {
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	return qbitDo("GET", path, "", nil)
}

func qbitForm(path string, data url.Values) (string, error) {
	return qbitDo("POST", path, "application/x-www-form-urlencoded", []byte(data.Encode()))
}

func qbitPost(path, contentType string, body []byte) (string, error) {
	return qbitDo("POST", path, contentType, body)
}

// qbitDo retries once through a fresh login on a 403, which is what an expired cookie looks like.
func qbitDo(method, path, contentType string, body []byte) (string, error) {
	qbitMu.Lock()
	defer qbitMu.Unlock()
	for attempt := 0; attempt < 2; attempt++ {
		request, err := http.NewRequest(method, qbitURL+"/api/v2/"+path, bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
		request.Header.Set("Referer", qbitURL)
		if qbitCookie != "" {
			request.Header.Set("Cookie", qbitCookie)
		}
		resp, err := client.Do(request)
		if err != nil {
			return "", err
		}
		payload, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusForbidden && attempt == 0 {
			if err := qbitLogin(); err != nil {
				return "", err
			}
			continue
		}
		if resp.StatusCode >= 300 {
			return "", fmt.Errorf("qbittorrent %s: %s", path, resp.Status)
		}
		return string(payload), readErr
	}
	return "", fmt.Errorf("qbittorrent %s: still forbidden after a login", path)
}
