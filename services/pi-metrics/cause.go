package main

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Why a session's audio is being re-encoded. Only `codec` says anything about the file: the client
// was willing to play it as it is and its profile rejected that audio codec. The others are about
// the client, and adding a track to the file changes nothing for them.
//
// Plex is the reason this exists. A client that turns direct play off itself (the Samsung app does,
// for some 4K files) gets a DASH stream whose profile takes only AAC, so EAC3 is re-encoded, and
// after an AC3 track is added the AC3 is re-encoded just the same.
const (
	causeCodec        = "codec"          // direct play was allowed and the codec was refused
	causeNoDirectPlay = "no_direct_play" // the client did not ask for direct play
	causeOther        = "other"          // Jellyfin named a reason that is not the audio codec
	causeUnknown      = "unknown"        // nothing says which
)

var (
	plexLogs      = env("PLEX_LOGS", "/plex-logs")
	plexLogFiles  = []string{"Plex Media Server.log", "Plex Media Server.1.log"}
	plexDirectArg = regexp.MustCompile(`[?&]directPlay=([01])`)

	causeMu    sync.Mutex
	plexCauses = map[string]string{}
)

// plexAudioCause reads it from Plex's log. Its sessions say a stream is transcoded and never whether
// the client asked for direct play; the decision request it logs does, and that request's session=
// is the last part of the TranscodeSession key. The request is logged at debug level, which is
// Plex's default; without it every Plex session is unknown and nothing is learned from Plex.
func plexAudioCause(id string) string {
	if id == "" || id == "." || id == "/" {
		return causeUnknown
	}
	causeMu.Lock()
	known, ok := plexCauses[id]
	causeMu.Unlock()
	if ok {
		return known
	}

	cause := causeUnknown
	needle := "session=" + id
	for _, name := range plexLogFiles {
		if found := lastDirectPlayArg(filepath.Join(plexLogs, name), needle); found != "" {
			cause = causeNoDirectPlay
			if found == "1" {
				cause = causeCodec
			}
			break
		}
	}
	if cause != causeUnknown {
		causeMu.Lock()
		if len(plexCauses) > 500 {
			plexCauses = map[string]string{}
		}
		plexCauses[id] = cause
		causeMu.Unlock()
	}
	return cause
}

func lastDirectPlayArg(file, needle string) string {
	handle, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer handle.Close()
	scanner := bufio.NewScanner(handle)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	last := ""
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, needle) || !strings.Contains(line, "Request:") ||
			!strings.Contains(line, "/transcode/universal/") {
			continue
		}
		if match := plexDirectArg.FindStringSubmatch(line); match != nil {
			last = match[1]
		}
	}
	return last
}

// jellyfinAudioCause takes Jellyfin's own TranscodeReasons, a list in the API and a comma-separated
// string in some versions.
func jellyfinAudioCause(raw any) string {
	var reasons []string
	switch value := raw.(type) {
	case []any:
		for _, item := range value {
			if text, ok := item.(string); ok {
				reasons = append(reasons, text)
			}
		}
	case string:
		reasons = strings.Split(value, ",")
	}
	if len(reasons) == 0 {
		return causeUnknown
	}
	for _, reason := range reasons {
		if strings.TrimSpace(reason) == "AudioCodecNotSupported" {
			return causeCodec
		}
	}
	return causeOther
}
