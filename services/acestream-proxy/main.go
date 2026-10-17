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
	chunkSize     = 32 * 1024
	// Chunks a viewer may fall behind before it is dropped: 8 MB, about 9 s of a 7 Mbit/s channel.
	// The chunks are shared, so this bounds memory per channel, not per viewer.
	viewerBacklog = 256
)

func main() {
	aceserveBase := getenv("ACESERVE_BASE_URL", "http://aceserve:6878")
	listenAddr := getenv("LISTEN_ADDR", ":6879")
	maxWait := getduration("MAX_WAIT", 45*time.Second)

	names := &channelNames{path: getenv("PLAYLIST_PATH", "/playlists/channels_ace.m3u")}
	log.Printf("acestream-proxy: %s -> %s (max wait %s, %d channel names from %s)",
		listenAddr, aceserveBase, maxWait, names.count(), names.path)

	h := newHub(aceserveBase, names, maxWait, getduration("LINGER", 10*time.Second))
	http.HandleFunc("/", h.serve)
	log.Fatal(http.ListenAndServe(listenAddr, nil))
}

// Aceserve answers 302 within half a second for every content id, whether or not a swarm exists,
// so its status code says nothing about whether the channel will play. The only honest signal is
// video actually arriving, which is what this waits for. Without it a dead channel leaves the
// player spinning forever with no error and nothing in the log.
//
// One upstream per channel, shared by every viewer. The engine serves a channel to one connection
// at a time, and Jellyfin opens two at once, one to play and one to probe the codecs: the probe got
// the 500, Jellyfin transcoded blind, and a Pi 4 re-encodes 1080p at half real time.
type hub struct {
	base    string
	names   *channelNames
	maxWait time.Duration
	linger  time.Duration
	client  *http.Client

	mu      sync.Mutex
	streams map[string]*stream
}

type stream struct {
	h     *hub
	id    string
	label string

	ready  chan struct{} // closed once the start has either produced video or failed
	ok     bool
	header http.Header

	mu      sync.Mutex
	viewers map[chan []byte]struct{}
	cancel  context.CancelFunc
	idle    *time.Timer
	done    bool
}

func newHub(base string, names *channelNames, maxWait, linger time.Duration) *hub {
	return &hub{base: base, names: names, maxWait: maxWait, linger: linger,
		client: &http.Client{}, streams: map[string]*stream{}}
}

func (h *hub) serve(w http.ResponseWriter, r *http.Request) {
	id := contentID(r.RequestURI)
	s, viewer, shared := h.join(id, r)
	if shared {
		log.Printf("[proxy] %s joined the running stream (%d watching)", s.label, s.count())
	} else {
		log.Printf("[proxy] %s requested", s.label)
	}
	defer s.leave(viewer)

	select {
	case <-s.ready:
	case <-r.Context().Done():
		return
	}
	if !s.ok {
		http.Error(w, "stream unavailable", http.StatusServiceUnavailable)
		return
	}

	for k, vals := range s.header {
		// A live stream has no length, and every viewer joins at its own point of it.
		if k == "Content-Length" || k == "Content-Range" {
			continue
		}
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	played := time.Now()
	for {
		select {
		case chunk, open := <-viewer:
			if !open {
				return
			}
			if _, err := w.Write(chunk); err != nil {
				log.Printf("[proxy] %s stream stopped after %s: %v", s.label, time.Since(played).Round(time.Second), err)
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		case <-r.Context().Done():
			log.Printf("[proxy] %s viewer left after %s", s.label, time.Since(played).Round(time.Second))
			return
		}
	}
}

// join returns the channel's stream, starting it if nobody is on it yet, plus a fresh viewer queue.
func (h *hub) join(id string, r *http.Request) (*stream, chan []byte, bool) {
	viewer := make(chan []byte, viewerBacklog)
	h.mu.Lock()
	defer h.mu.Unlock()

	if s, ok := h.streams[id]; ok {
		s.mu.Lock()
		if !s.done {
			s.viewers[viewer] = struct{}{}
			if s.idle != nil {
				s.idle.Stop()
				s.idle = nil
			}
			s.mu.Unlock()
			return s, viewer, true
		}
		s.mu.Unlock()
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &stream{h: h, id: id, label: h.names.label(id), ready: make(chan struct{}),
		viewers: map[chan []byte]struct{}{viewer: {}}, cancel: cancel}
	h.streams[id] = s

	headers := r.Header.Clone()
	headers.Del("Range") // live: there is nothing to seek to, and joiners never asked for it
	go s.run(ctx, h.base+r.RequestURI, headers)
	return s, viewer, false
}

func (s *stream) run(ctx context.Context, target string, headers http.Header) {
	defer s.finish()
	start := time.Now()
	deadline := start.Add(s.h.maxWait)

	resp, ok := open(ctx, s.h.client, target, headers, s.label, deadline)
	if !ok {
		close(s.ready)
		return
	}
	defer resp.Body.Close()

	first, ok := awaitFirstBytes(ctx, resp.Body, s.label, start, deadline)
	if !ok {
		if ctx.Err() == nil {
			log.Printf("[proxy] %s gave up, no video after %s", s.label, s.h.maxWait)
		}
		close(s.ready)
		return
	}
	log.Printf("[proxy] %s playing after %s", s.label, time.Since(start).Round(time.Second))
	s.ok, s.header = true, resp.Header.Clone()
	close(s.ready)
	s.broadcast(first)

	for {
		// Whole chunks, so the backlog is the 8 MB it claims: the engine's reads come in a few KB at
		// a time, and 256 of those filled in the opening burst and dropped the TV the second it began.
		buf := make([]byte, chunkSize)
		n, err := io.ReadFull(resp.Body, buf)
		if n > 0 {
			s.broadcast(buf[:n])
		}
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("[proxy] %s engine stream ended after %s: %v", s.label, time.Since(start).Round(time.Second), err)
			}
			return
		}
	}
}

// broadcast never blocks on a viewer: one that fell a whole backlog behind is dropped, so a stalled
// client cannot freeze the channel for everyone else.
func (s *stream) broadcast(chunk []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for v := range s.viewers {
		select {
		case v <- chunk:
		default:
			log.Printf("[proxy] %s dropped a viewer that fell %d chunks behind", s.label, viewerBacklog)
			delete(s.viewers, v)
			close(v)
		}
	}
}

// leave lets the upstream go once the last viewer has been gone for the linger period: Jellyfin
// closes and reopens within seconds when it switches from probing to playing.
func (s *stream) leave(viewer chan []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.viewers[viewer]; ok {
		delete(s.viewers, viewer)
	}
	if len(s.viewers) > 0 || s.done || s.idle != nil {
		return
	}
	s.idle = time.AfterFunc(s.h.linger, func() {
		s.mu.Lock()
		empty := len(s.viewers) == 0
		s.mu.Unlock()
		if empty {
			s.cancel()
		}
	})
}

func (s *stream) finish() {
	s.h.mu.Lock()
	if s.h.streams[s.id] == s {
		delete(s.h.streams, s.id)
	}
	s.h.mu.Unlock()

	s.mu.Lock()
	s.done = true
	for v := range s.viewers {
		close(v)
	}
	s.viewers = map[chan []byte]struct{}{}
	if s.idle != nil {
		s.idle.Stop()
	}
	s.mu.Unlock()
	s.cancel()
}

func (s *stream) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.viewers)
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
