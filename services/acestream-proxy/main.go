package main

import (
	"io"
	"log"
	"net/http"
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
		log.Printf("[proxy] %s", r.RequestURI)

		// Poll aceserve until it's ready (anything other than 500)
		deadline := time.Now().Add(maxWait)
		for {
			resp, err := probeClient.Get(target)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode != http.StatusInternalServerError {
					break
				}
			}
			if time.Now().After(deadline) {
				log.Printf("[proxy] stream not ready after %s", maxWait)
				http.Error(w, "stream unavailable", http.StatusServiceUnavailable)
				return
			}
			log.Printf("[proxy] aceserve not ready, retrying in %s...", retryInterval)
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
			log.Printf("[proxy] stream request failed: %v", err)
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
		if _, err := io.Copy(w, resp.Body); err != nil {
			log.Printf("[proxy] stream copy stopped: %v", err)
		}
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
