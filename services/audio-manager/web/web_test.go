package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path, contentType, cache, contains string
		code                               int
	}{
		{"/", "text/html; charset=utf-8", "", "<title>Audio manager</title>", http.StatusOK},
		{"/app.js", "text/javascript; charset=utf-8", "", "function refresh()", http.StatusOK},
		{"/style.css", "text/css; charset=utf-8", "", ":root {", http.StatusOK},
		{"/icon.svg", "image/svg+xml", "public, max-age=86400", "<svg", http.StatusOK},
		{"/apple-touch-icon.png", "image/png", "public, max-age=86400", "PNG", http.StatusOK},
		{"/index.html", "text/plain; charset=utf-8", "", "404", http.StatusNotFound},
		{"/api/unknown", "text/plain; charset=utf-8", "", "404", http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			Handler().ServeHTTP(w, httptest.NewRequest("GET", c.path, nil))
			if w.Code != c.code || w.Header().Get("Content-Type") != c.contentType || w.Header().Get("Cache-Control") != c.cache ||
				!strings.Contains(w.Body.String(), c.contains) {
				t.Errorf("%d %q %q, body starts %.40q", w.Code, w.Header().Get("Content-Type"), w.Header().Get("Cache-Control"), w.Body.String())
			}
		})
	}
}

func TestThePageLoadsItsOwnAssets(t *testing.T) {
	t.Parallel()
	page := string(indexHTML)
	for path := range assets {
		if path != "/" && !strings.Contains(page, `"`+path+`"`) {
			t.Errorf("index.html does not reference %s", path)
		}
	}
}
