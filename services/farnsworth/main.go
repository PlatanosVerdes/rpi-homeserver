package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Hands a channel to VLC and gets out of the way: the video never passes through here, it goes
// straight from the engine to the player. Pointing at the engine rather than at bifrost's proxy is
// deliberate, see docs/farnsworth.md.
func main() {
	listenAddr := getenv("LISTEN_ADDR", ":8086")
	streamBase := strings.TrimRight(getenv("STREAM_BASE", "http://192.168.1.180:6878"), "/")
	playlist := &playlist{url: strings.TrimRight(getenv("BIFROST_URL", "http://bifrost:6879"), "/") + "/api/channels"}

	s := &server{playlist: playlist, streamBase: streamBase}
	http.HandleFunc("/", s.index)
	http.HandleFunc("/m3u/", s.m3u)
	http.HandleFunc("/all.m3u", s.all)
	http.HandleFunc("/click/", s.click)
	http.HandleFunc("/icon.svg", icon)

	log.Printf("farnsworth: %s, streams at %s, %d channels from %s",
		listenAddr, streamBase, len(playlist.channels()), playlist.url)
	log.Fatal(http.ListenAndServe(listenAddr, nil))
}

type server struct {
	playlist   *playlist
	streamBase string
}

func (s *server) streamURL(id string) string {
	return s.streamBase + "/ace/getstream?id=" + id
}

func (s *server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	render(w, s.playlist.grouped(), s.streamBase)
}

// A one-channel playlist, which is what desktop players open. Serving it counts as a play, so it
// is logged like a tap on the button.
func (s *server) m3u(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/m3u/")
	ch, ok := s.playlist.byID(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.logPlay(r, ch, "m3u")

	w.Header().Set("Content-Type", "audio/x-mpegurl")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+safeFilename(ch.Name)+".m3u\"")
	w.Write([]byte("#EXTM3U\n#EXTINF:-1," + ch.Name + "\n" + s.streamURL(ch.ID) + "\n"))
}

// The whole list, to be added once in whatever player the device already has. VLC's URL scheme is
// unreliable on iOS (their own tracker has "vlc-x-callback not works" and "working at most twice"),
// so the dependable path is handing the player a playlist and letting it own the experience.
func (s *server) all(w http.ResponseWriter, r *http.Request) {
	chans := s.playlist.channels()
	log.Printf("[playlist] %d channels served to %s", len(chans), clientIP(r))

	w.Header().Set("Content-Type", "audio/x-mpegurl")
	w.Header().Set("Content-Disposition", "attachment; filename=\"farnsworth.m3u\"")
	w.Write([]byte("#EXTM3U\n"))
	for _, c := range chans {
		line := "#EXTINF:-1"
		if c.Logo != "" {
			line += " tvg-logo=\"" + c.Logo + "\""
		}
		if c.Group != "" {
			line += " group-title=\"" + c.Group + "\""
		}
		w.Write([]byte(line + "," + c.Name + "\n" + s.streamURL(c.ID) + "\n"))
	}
}

// The button jumps straight to VLC's own URL scheme, which never reaches a server, so the page
// pings this first. Beacons are fire-and-forget: answer and say nothing.
func (s *server) click(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/click/")
	if ch, ok := s.playlist.byID(id); ok {
		s.logPlay(r, ch, "vlc")
	}
	w.WriteHeader(http.StatusNoContent)
}

// The whole point of routing plays through this page rather than handing out the engine's address:
// the client here is the actual device, so who watched what is answerable. Through Jellyfin every
// request looks like Jellyfin.
func (s *server) logPlay(r *http.Request, ch channel, how string) {
	log.Printf("[play] %s (%s) via %s from %s", ch.Name, short(ch.ID), how, clientIP(r))
}

func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return strings.TrimSpace(strings.Split(fwd, ",")[0])
	}
	if host, _, err := splitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func splitHostPort(addr string) (string, string, error) {
	u, err := url.Parse("//" + addr)
	if err != nil {
		return "", "", err
	}
	return u.Hostname(), u.Port(), nil
}

type channel struct {
	ID    string
	Name  string
	Logo  string
	Group string
}

type group struct {
	Name     string
	Channels []channel
}

// playlist is bifrost's catalog. Jellyfin's M3U only carries bifrost's stable channel URLs, so the
// content ids the engine needs come from /api/channels, each channel's last working source first.
type playlist struct {
	url string

	mu      sync.RWMutex
	items   []channel
	fetched time.Time
}

func (p *playlist) channels() []channel {
	p.reload()
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.items
}

func (p *playlist) byID(id string) (channel, bool) {
	for _, c := range p.channels() {
		if c.ID == id {
			return c, true
		}
	}
	return channel{}, false
}

// Groups keep the order the list gives them, since it is curated; channels inside a group are sorted
// by name. bifrost has already folded each channel's sources into one entry.
func (p *playlist) grouped() []group {
	var out []group
	index := map[string]int{}
	for _, c := range p.channels() {
		name := c.Group
		if name == "" {
			name = "Sin grupo"
		}
		i, ok := index[name]
		if !ok {
			index[name] = len(out)
			out = append(out, group{Name: name})
			i = len(out) - 1
		}
		out[i].Channels = append(out[i].Channels, c)
	}
	for i := range out {
		sort.SliceStable(out[i].Channels, func(a, b int) bool {
			return out[i].Channels[a].Name < out[i].Channels[b].Name
		})
	}
	return out
}

// The catalog follows the list every few minutes; once a minute is fresh enough for a page.
func (p *playlist) reload() {
	p.mu.RLock()
	fresh := time.Since(p.fetched) < time.Minute
	p.mu.RUnlock()
	if fresh {
		return
	}

	items, err := fetch(p.url)
	if err != nil {
		log.Printf("farnsworth: cannot read %s: %v", p.url, err)
		return
	}
	p.mu.Lock()
	p.items, p.fetched = items, time.Now()
	p.mu.Unlock()
}

func fetch(url string) ([]channel, error) {
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var body struct {
		Channels []struct {
			Name  string   `json:"name"`
			Group string   `json:"group"`
			Logo  string   `json:"logo"`
			IDs   []string `json:"ids"`
		} `json:"channels"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := make([]channel, 0, len(body.Channels))
	for _, c := range body.Channels {
		if len(c.IDs) > 0 {
			out = append(out, channel{ID: c.IDs[0], Name: c.Name, Logo: c.Logo, Group: c.Group})
		}
	}
	return out, nil
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func safeFilename(name string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return '-'
		}
		return r
	}, name)
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
