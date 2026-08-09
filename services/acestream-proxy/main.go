package main

import (
	"bufio"
	"context"
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
	logEvery      = 10 * time.Second // progress lines, so a 45s wait is 4 lines and not 22
	firstReadSize = 64 * 1024
)

func main() {
	aceserveBase := getenv("ACESERVE_BASE_URL", "http://aceserve:6878")
	listenAddr := getenv("LISTEN_ADDR", ":6879")
	maxWait := getduration("MAX_WAIT", 45*time.Second)

	names := &channelNames{path: getenv("PLAYLIST_PATH", "/playlists/channels_ace.m3u")}
	log.Printf("acestream-proxy: %s -> %s (max wait %s, %d channel names from %s)",
		listenAddr, aceserveBase, maxWait, names.count(), names.path)

	http.HandleFunc("/", makeHandler(aceserveBase, names, maxWait))
	log.Fatal(http.ListenAndServe(listenAddr, nil))
}

// Aceserve answers 302 within half a second for every content id, whether or not a swarm exists,
// so its status code says nothing about whether the channel will play. The only honest signal is
// video actually arriving, which is what this waits for. Without it a dead channel leaves the
// player spinning forever with no error and nothing in the log.
func makeHandler(aceserveBase string, names *channelNames, maxWait time.Duration) http.HandlerFunc {
	client := &http.Client{} // follows the 302 to the stream, no timeout of its own

	return func(w http.ResponseWriter, r *http.Request) {
		target := aceserveBase + r.RequestURI
		ch := names.label(contentID(r.RequestURI))
		log.Printf("[proxy] %s requested", ch)

		start := time.Now()
		deadline := start.Add(maxWait)

		resp, ok := open(r.Context(), client, target, r.Header, ch, deadline)
		if !ok {
			http.Error(w, "engine unavailable", http.StatusServiceUnavailable)
			return
		}
		defer resp.Body.Close()

		first, ok := awaitFirstBytes(r.Context(), resp.Body, ch, start, deadline)
		if !ok {
			log.Printf("[proxy] %s gave up, no video after %s", ch, maxWait)
			http.Error(w, "stream unavailable", http.StatusServiceUnavailable)
			return
		}
		log.Printf("[proxy] %s playing after %s", ch, time.Since(start).Round(time.Second))

		for k, vals := range resp.Header {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)

		played := time.Now()
		if _, err := w.Write(first); err != nil {
			log.Printf("[proxy] %s client left immediately: %v", ch, err)
			return
		}
		if _, err := io.Copy(w, resp.Body); err != nil {
			log.Printf("[proxy] %s stream stopped after %s: %v", ch, time.Since(played).Round(time.Second), err)
		} else {
			log.Printf("[proxy] %s stream ended after %s", ch, time.Since(played).Round(time.Second))
		}
	}
}

// Aceserve answers 500 while it cannot get a stream going at all, which covers both an engine
// still starting and a content id it fails to resolve. Either is worth retrying. Anything else is
// answered straight away and judged on whether video follows.
func open(ctx context.Context, client *http.Client, target string, headers http.Header, ch string, deadline time.Time) (*http.Response, bool) {
	var lastLog time.Time
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			log.Printf("[proxy] %s bad request: %v", ch, err)
			return nil, false
		}
		for k, vals := range headers {
			for _, v := range vals {
				req.Header.Add(k, v)
			}
		}

		resp, err := client.Do(req)
		if err == nil && resp.StatusCode != http.StatusInternalServerError {
			return resp, true
		}
		if resp != nil {
			resp.Body.Close()
		}
		if ctx.Err() != nil {
			log.Printf("[proxy] %s abandoned before it started", ch)
			return nil, false
		}
		if time.Now().After(deadline) {
			log.Printf("[proxy] %s gave up, the engine never produced a stream", ch)
			return nil, false
		}
		if time.Since(lastLog) >= logEvery {
			log.Printf("[proxy] %s no stream yet, retrying", ch)
			lastLog = time.Now()
		}
		time.Sleep(retryInterval)
	}
}

// Reads in its own goroutine so the wait can be reported while it blocks. Closing the body on the
// way out unblocks that read, and the channel is buffered so it never leaks.
func awaitFirstBytes(ctx context.Context, body io.Reader, ch string, start, deadline time.Time) ([]byte, bool) {
	type result struct {
		buf []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		buf := make([]byte, firstReadSize)
		n, err := io.ReadAtLeast(body, buf, 1)
		done <- result{buf[:n], err}
	}()

	ticker := time.NewTicker(logEvery)
	defer ticker.Stop()
	timeout := time.NewTimer(time.Until(deadline))
	defer timeout.Stop()

	for {
		select {
		case res := <-done:
			if res.err != nil && len(res.buf) == 0 {
				log.Printf("[proxy] %s stream closed without sending anything: %v", ch, res.err)
				return nil, false
			}
			return res.buf, true
		case <-ticker.C:
			log.Printf("[proxy] %s waiting for the swarm, %s of %s elapsed",
				ch, time.Since(start).Round(time.Second), deadline.Sub(start))
		case <-ctx.Done():
			log.Printf("[proxy] %s abandoned while waiting, after %s", ch, time.Since(start).Round(time.Second))
			return nil, false
		case <-timeout.C:
			return nil, false
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

func getduration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		log.Printf("[proxy] %s=%q is not a duration, using %s", key, v, def)
	}
	return def
}
