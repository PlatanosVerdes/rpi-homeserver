package main

import (
	"bufio"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Hands a channel to VLC and gets out of the way: the video never passes through here, it goes
// straight from the engine to the player. Pointing at the engine rather than at acestream-proxy is
// deliberate, see docs/farnsworth.md.
func main() {
	listenAddr := getenv("LISTEN_ADDR", ":8086")
	streamBase := strings.TrimRight(getenv("STREAM_BASE", "http://192.168.1.180:6878"), "/")
	playlist := &playlist{path: getenv("PLAYLIST_PATH", "/playlists/channels_ace.m3u")}

	s := &server{playlist: playlist, streamBase: streamBase}
	http.HandleFunc("/", s.index)
	http.HandleFunc("/m3u/", s.m3u)
	http.HandleFunc("/click/", s.click)
	http.HandleFunc("/icon.svg", icon)

	log.Printf("farnsworth: %s, streams at %s, %d channels from %s",
		listenAddr, streamBase, len(playlist.channels()), playlist.path)
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

type playlist struct {
	path string

	mu      sync.RWMutex
	items   []channel
	modTime time.Time
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

// Groups keep the order the playlist gives them, since it is curated; channels inside a group are
// sorted so the two entries of the same channel from different sources end up next to each other.
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

// The updater rewrites the playlist, so pick up a new one without a restart.
func (p *playlist) reload() {
	st, err := os.Stat(p.path)
	if err != nil {
		return
	}
	p.mu.RLock()
	fresh := st.ModTime().Equal(p.modTime)
	p.mu.RUnlock()
	if fresh {
		return
	}

	items, err := parse(p.path)
	if err != nil {
		log.Printf("farnsworth: cannot read %s: %v", p.path, err)
		return
	}
	p.mu.Lock()
	p.items, p.modTime = items, st.ModTime()
	p.mu.Unlock()
}

var attr = regexp.MustCompile(`([a-zA-Z-]+)="([^"]*)"`)

func parse(path string) ([]channel, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []channel
	var pending channel
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "#EXTINF"):
			pending = channel{}
			for _, m := range attr.FindAllStringSubmatch(line, -1) {
				switch m[1] {
				case "tvg-logo":
					pending.Logo = m[2]
				case "group-title":
					pending.Group = m[2]
				}
			}
			if i := strings.LastIndex(line, ","); i >= 0 {
				pending.Name = strings.TrimSpace(line[i+1:])
			}
		case line != "" && !strings.HasPrefix(line, "#"):
			if id := contentID(line); id != "" && pending.Name != "" {
				pending.ID = id
				out = append(out, pending)
			}
			pending = channel{}
		}
	}
	return out, sc.Err()
}

func contentID(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Query().Get("id")
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
