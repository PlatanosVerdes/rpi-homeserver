package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// While anything plays on Jellyfin, qBittorrent runs on its alternative speed limits, so downloads
// stop taking the Pi's CPU and line from the picture. Throttled and not paused: private trackers
// still need the seeding.

var (
	jellyfinURL = env("JELLYFIN_URL", "http://host.docker.internal:8096")
	quietDown   = env("QUIET_DOWN_KIB", "3072")
	quietUp     = env("QUIET_UP_KIB", "1024")
	// A pause, a channel change or the next episode is not the end of watching.
	quietLinger = 2 * time.Minute

	quietOn      atomic.Bool  // the limits are on because of playback
	quietPlaying atomic.Int64 // sessions playing at the last look
)

func quietWhileWatching(every time.Duration) {
	if err := setQuietLimits(); err != nil {
		logLine(fmt.Sprintf("quiet: could not set the alternative limits: %v", err))
	}
	var lastPlaying time.Time
	for ; ; time.Sleep(every) {
		lastPlaying = quietStep(lastPlaying, time.Now())
	}
}

// quietStep looks once and moves the limits; it answers when something last played.
func quietStep(lastPlaying, now time.Time) time.Time {
	playing, err := jellyfinPlaying()
	if err != nil {
		logLine(fmt.Sprintf("quiet: Jellyfin: %v", err))
		return lastPlaying
	}
	quietPlaying.Store(int64(playing))
	if playing > 0 {
		lastPlaying = now
	}
	want := !lastPlaying.IsZero() && now.Sub(lastPlaying) < quietLinger
	on, err := qbitAltLimits()
	if err != nil {
		logLine(fmt.Sprintf("quiet: qBittorrent: %v", err))
		return lastPlaying
	}
	switch {
	case want && !on:
		if toggleAltLimits(fmt.Sprintf("%d playing on Jellyfin", playing)) {
			quietOn.Store(true)
		}
	// Only lifts limits it set: someone who turned them on by hand keeps them.
	case !want && on && quietOn.Load():
		if toggleAltLimits("nothing playing") {
			quietOn.Store(false)
		}
	case !want && !on:
		quietOn.Store(false)
	}
	return lastPlaying
}

func setQuietLimits() error {
	down, err1 := strconv.Atoi(quietDown)
	up, err2 := strconv.Atoi(quietUp)
	if err1 != nil || err2 != nil {
		return fmt.Errorf("QUIET_DOWN_KIB and QUIET_UP_KIB must be numbers")
	}
	if dryRun {
		return nil
	}
	prefs, _ := json.Marshal(map[string]int{"alt_dl_limit": down * 1024, "alt_up_limit": up * 1024})
	_, err := qbitPost("app/setPreferences", url.Values{"json": {string(prefs)}})
	return err
}

func qbitAltLimits() (bool, error) {
	mode, err := qbitCall("transfer/speedLimitsMode", nil)
	return strings.TrimSpace(mode) == "1", err
}

func toggleAltLimits(why string) bool {
	if dryRun {
		logLine("quiet: would toggle qBittorrent's alternative limits: " + why)
		return false
	}
	if _, err := qbitPost("transfer/toggleSpeedLimitsMode", url.Values{}); err != nil {
		logLine(fmt.Sprintf("quiet: could not toggle qBittorrent's alternative limits: %v", err))
		return false
	}
	logLine("quiet: toggled qBittorrent's alternative limits: " + why)
	return true
}

// jellyfinPlaying counts the sessions playing something now, paused ones left out.
func jellyfinPlaying() (int, error) {
	request, err := http.NewRequest("GET", strings.TrimRight(jellyfinURL, "/")+"/Sessions?activeWithinSeconds=60", nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("X-Emby-Token", env("JELLYFIN_API_KEY", ""))
	resp, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("/Sessions: %s", resp.Status)
	}
	var sessions []struct {
		NowPlayingItem *struct{ Name string }
		PlayState      struct{ IsPaused bool }
	}
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		return 0, err
	}
	playing := 0
	for _, s := range sessions {
		if s.NowPlayingItem != nil && !s.PlayState.IsPaused {
			playing++
		}
	}
	return playing, nil
}
