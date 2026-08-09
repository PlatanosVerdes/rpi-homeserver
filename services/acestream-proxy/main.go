package main

import (
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
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

	http.HandleFunc("/", makeHandler(aceserveBase))
	log.Printf("acestream-proxy: %s -> %s", listenAddr, aceserveBase)
	log.Fatal(http.ListenAndServe(listenAddr, nil))
}

func makeHandler(aceserveBase string) http.HandlerFunc {
	probeClient := &http.Client{
		Timeout: probeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // don't follow — just check status
		},
	}
	streamClient := &http.Client{} // follows redirects, no timeout (streaming)

	return func(w http.ResponseWriter, r *http.Request) {
		target := aceserveBase + r.RequestURI
		id := contentID(r.RequestURI)
		log.Printf("[proxy] %s requested", id)

		// Poll aceserve until it's ready (anything other than 500)
		start := time.Now()
		deadline := start.Add(maxWait)
		for {
			resp, err := probeClient.Get(target)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode != http.StatusInternalServerError {
					if waited := time.Since(start); waited >= retryInterval {
						log.Printf("[proxy] %s ready after %s", id, waited.Round(time.Second))
					} else {
						log.Printf("[proxy] %s ready immediately", id)
					}
					break
				}
			}
			if time.Now().After(deadline) {
				log.Printf("[proxy] %s gave up, no stream after %s", id, maxWait)
				http.Error(w, "stream unavailable", http.StatusServiceUnavailable)
				return
			}
			log.Printf("[proxy] %s waiting for the swarm, %s of %s elapsed",
				id, time.Since(start).Round(time.Second), maxWait)
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
			log.Printf("[proxy] %s stream request failed: %v", id, err)
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
			log.Printf("[proxy] %s stream stopped after %s: %v", id, time.Since(played).Round(time.Second), err)
		} else {
			log.Printf("[proxy] %s stream ended after %s", id, time.Since(played).Round(time.Second))
		}
	}
}

// Every log line carries the channel, so a wait or a failure can be attributed to one.
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
