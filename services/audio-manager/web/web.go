// Package web serves the page: its HTML, script, stylesheet and icons, embedded in the binary.
package web

import (
	_ "embed"
	"log"
	"net/http"
)

var (
	//go:embed index.html
	indexHTML []byte
	//go:embed app.js
	appJS []byte
	//go:embed style.css
	styleCSS []byte
	// A waveform whose bars light up from left to right: the audio before and after the conversion.
	// Thick rounded bars and no detail finer than 4 units, since Homepage draws it at 34 px.
	//go:embed icon.svg
	iconSVG []byte
	// iOS ignores SVG icons on the home screen, so it gets this PNG of icon.svg, its background squared
	// because iOS rounds the corners itself.
	//go:embed apple-touch-icon.png
	touchIcon []byte
)

//go:generate sh -c "echo 'svg > rect:first-of-type { rx: 0; ry: 0 }' | rsvg-convert --width 180 --height 180 --stylesheet /dev/stdin icon.svg -o apple-touch-icon.png"

type asset struct {
	body        []byte
	contentType string
	cache       string
}

var assets = map[string]asset{
	"/":                     {indexHTML, "text/html; charset=utf-8", ""},
	"/app.js":               {appJS, "text/javascript; charset=utf-8", ""},
	"/style.css":            {styleCSS, "text/css; charset=utf-8", ""},
	"/icon.svg":             {iconSVG, "image/svg+xml", "public, max-age=86400"},
	"/apple-touch-icon.png": {touchIcon, "image/png", "public, max-age=86400"},
}

// Handler serves the page and its assets, and a 404 for any other path.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := assets[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", a.contentType)
		if a.cache != "" {
			w.Header().Set("Cache-Control", a.cache)
		}
		if _, err := w.Write(a.body); err != nil {
			log.Printf("audio-manager: serving %s failed: %v", r.URL.Path, err)
		}
	})
}
