package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestQuietFollowsPlayback(t *testing.T) {
	var mu sync.Mutex
	playing, alt, toggles := 0, false, 0
	jellyfin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if playing > 0 {
			fmt.Fprint(w, `[{"NowPlayingItem":{"Name":"DAZN F1 1080p"},"PlayState":{"IsPaused":false}},{"NowPlayingItem":{"Name":"A film"},"PlayState":{"IsPaused":true}}]`)
			return
		}
		fmt.Fprint(w, `[{"PlayState":{}}]`)
	}))
	defer jellyfin.Close()
	qbit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/v2/transfer/speedLimitsMode":
			fmt.Fprint(w, map[bool]string{true: "1", false: "0"}[alt])
		case "/api/v2/transfer/toggleSpeedLimitsMode":
			alt = !alt
			toggles++
		}
	}))
	defer qbit.Close()
	jellyfinURL, qbitURL = jellyfin.URL, qbit.URL
	quietOn.Store(false)
	set := func(p int) { mu.Lock(); playing = p; mu.Unlock() }
	state := func() (bool, int) { mu.Lock(); defer mu.Unlock(); return alt, toggles }

	t0 := time.Now()
	last := quietStep(time.Time{}, t0)
	if on, _ := state(); on {
		t.Fatal("limits on with nothing playing")
	}
	set(1)
	last = quietStep(last, t0.Add(time.Minute))
	if on, _ := state(); !on || quietPlaying.Load() != 1 {
		t.Fatalf("limits on %t with %d counted, want on with the paused session left out", on, quietPlaying.Load())
	}
	set(0)
	last = quietStep(last, t0.Add(2*time.Minute))
	if on, _ := state(); !on {
		t.Error("limits lifted a minute after playback stopped, want them kept through a pause")
	}
	last = quietStep(last, t0.Add(4*time.Minute))
	if on, n := state(); on || n != 2 {
		t.Errorf("limits on %t after %d toggles, want off once nothing played for %s", on, n, quietLinger)
	}

	mu.Lock()
	alt = true // turned on by hand
	mu.Unlock()
	quietStep(last, t0.Add(10*time.Minute))
	if on, _ := state(); !on {
		t.Error("lifted limits someone turned on by hand")
	}
}
