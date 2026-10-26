package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// logo serves a catalogue icon from here: they are http URLs, which an https page refuses to load.
// Only URLs the catalogue names are fetched, so this is no open proxy.
func (a *app) logo(w http.ResponseWriter, r *http.Request) {
	u := r.URL.Query().Get("u")
	if !a.store.knownIcon(u) {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(a.store.dir, "logos", fmt.Sprintf("%x", sha256.Sum256([]byte(u))))
	b, err := os.ReadFile(path)
	if err != nil {
		if b, err = a.fetchLogo(u); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if os.MkdirAll(filepath.Dir(path), 0o755) == nil {
			writeFile(path, b)
		}
	}
	w.Header().Set("Content-Type", http.DetectContentType(b))
	w.Header().Set("Cache-Control", "public, max-age=604800")
	w.Write(b)
}

func (a *app) fetchLogo(u string) ([]byte, error) {
	resp, err := a.web.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
