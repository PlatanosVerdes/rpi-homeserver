package main

import (
	"bufio"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	retryInterval = 2 * time.Second
	maxWait       = 45 * time.Second
	probeTimeout  = 3 * time.Second
)

func main() {
	aceserveBase := getenv("ACESERVE_BASE_URL", "http://aceserve:6878")
	listenAddr := getenv("LISTEN_ADDR", ":6879")

	names := &channelNames{path: getenv("PLAYLIST_PATH", "/playlists/channels_ace.m3u")}
	log.Printf("acestream-proxy: %s -> %s (%d channel names from %s)",
		listenAddr, aceserveBase, names.count(), names.path)

	http.HandleFunc("/", makeHandler(aceserveBase, names))
	log.Fatal(http.ListenAndServe(listenAddr, nil))
}

func makeHandler(aceserveBase string, names *channelNames) http.HandlerFunc {
	probeClient := &http.Client{
		Timeout: probeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // don't follow — just check status
		},
	}
	streamClient := &http.Client{} // follows redirects, no timeout (streaming)

	return func(w http.ResponseWriter, r *http.Request) {
		target := aceserveBase + r.RequestURI
		ch := names.label(contentID(r.RequestURI))
		log.Printf("[proxy] %s requested", ch)

		// Poll aceserve until it's ready (anything other than 500)
		start := time.Now()
		deadline := start.Add(maxWait)
		for {
			resp, err := probeClient.Get(target)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode != http.StatusInternalServerError {
					if waited := time.Since(start); waited >= retryInterval {
						log.Printf("[proxy] %s ready after %s", ch, waited.Round(time.Second))
					} else {
						log.Printf("[proxy] %s ready immediately", ch)
					}
					break
				}
			}
			if time.Now().After(deadline) {
				log.Printf("[proxy] %s gave up, no stream after %s", ch, maxWait)
				http.Error(w, "stream unavailable", http.StatusServiceUnavailable)
				return
			}
			log.Printf("[proxy] %s waiting for the swarm, %s of %s elapsed",
				ch, time.Since(start).Round(time.Second), maxWait)
			time.Sleep(retryInterval)
		}

		// Stream the actual data (streamClient follows the 302 redirect from aceserve)
		req, err := http.NewRequest(http.MethodGet, target, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for k, vals := range r.Header {
			for _, v := range vals {
				req.Header.Add(k, v)
			}
		}

		resp, err := streamClient.Do(req)
		if err != nil {
			log.Printf("[proxy] %s stream request failed: %v", ch, err)
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		for k, vals := range resp.Header {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		played := time.Now()
		if _, err := io.Copy(w, resp.Body); err != nil {
			log.Printf("[proxy] %s stream stopped after %s: %v", ch, time.Since(played).Round(time.Second), err)
		} else {
			log.Printf("[proxy] %s stream ended after %s", ch, time.Since(played).Round(time.Second))
		}
	}
}

// Content ids are 40 hex characters and unreadable in a log, so lines are labelled with the
// channel name from the playlist Jellyfin reads, keeping a short id to tell duplicates apart:
// several entries share a name and differ only by source.
type channelNames struct {
	path string

	mu      sync.RWMutex
	byID    map[string]string
	modTime time.Time
}

func (c *channelNames) label(id string) string {
	c.reload()
	c.mu.RLock()
	name := c.byID[id]
	c.mu.RUnlock()

	short := id
	if len(short) > 8 {
		short = short[:8]
	}
	if name == "" {
		return short
	}
	return name + " (" + short + ")"
}

func (c *channelNames) count() int {
	c.reload()
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.byID)
}

// The updater rewrites the playlist periodically, so pick up a new one without a restart.
func (c *channelNames) reload() {
	st, err := os.Stat(c.path)
	if err != nil {
		return
	}
	c.mu.RLock()
	fresh := st.ModTime().Equal(c.modTime)
	c.mu.RUnlock()
	if fresh {
		return
	}

	parsed, err := parsePlaylist(c.path)
	if err != nil {
		log.Printf("[proxy] cannot read the playlist at %s: %v", c.path, err)
		return
	}
	c.mu.Lock()
	c.byID, c.modTime = parsed, st.ModTime()
	c.mu.Unlock()
}

func parsePlaylist(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	names := map[string]string{}
	var pending string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "#EXTINF"):
			if i := strings.LastIndex(line, ","); i >= 0 {
				pending = strings.TrimSpace(line[i+1:])
			}
		case line != "" && !strings.HasPrefix(line, "#"):
			if id := contentID(line); id != "unknown" && pending != "" {
				names[id] = pending
			}
			pending = ""
		}
	}
	return names, sc.Err()
}

func contentID(requestURI string) string {
	u, err := url.Parse(requestURI)
	if err != nil {
		return "unknown"
	}
	if id := u.Query().Get("id"); id != "" {
		return id
	}
	return "unknown"
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
