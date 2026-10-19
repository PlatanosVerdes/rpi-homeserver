package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// Which audio codecs a real client has had re-encoded, and on which files, kept on disk for
// scripts/sync/audio-compat.py. That script converts what this has watched fail, rather than what a
// list in git guesses a TV cannot decode.
//
// A session only counts when its client is not a browser and its video was copied. A browser
// re-encodes every surround codec and nothing on this end changes that. When the video is
// re-encoded too, the audio goes along with it whatever its codec, because the client asked for a
// lower bitrate or a container it can take: Jellyfin's Live TV re-encodes aac that way every day,
// and learning from it would mark aac, the codec every client plays, as one none of them can.
//
// Nothing here forgets a codec on its own. One that a client update fixed is removed from the file
// by hand and the container restarted, since what is in memory is written back on the next change.

type codecSeen struct {
	FirstSeen int64    `json:"first_seen"`
	LastSeen  int64    `json:"last_seen"`
	Clients   []string `json:"clients"`
}

type fileSeen struct {
	Path   string `json:"path"`
	Codec  string `json:"codec"`
	App    string `json:"app"`
	Client string `json:"client"`
	Seen   int64  `json:"seen"`
}

type audioLedger struct {
	Codecs map[string]*codecSeen `json:"codecs"`
	Files  []fileSeen            `json:"files"` // newest first
}

// The files are a queue for the next night, not a history: audio-compat only needs the latest few.
const ledgerFileLimit = 100

var (
	ledgerMu   sync.Mutex
	ledger     = audioLedger{Codecs: map[string]*codecSeen{}}
	ledgerFile = filepath.Join(env("STATE_PATH", "/state"), "audio-recodes.json")
)

// learnable is the source audio codec of a session that says something about the client, or "".
func learnable(current session) string {
	if current.browser {
		return ""
	}
	if _, video := current.recodes[streamVideo]; video {
		return ""
	}
	codec := strings.ToLower(current.recodes[streamAudio])
	if codec == "?" {
		return ""
	}
	return codec
}

// record folds the sessions into the ledger and says whether anything in it changed.
func (l *audioLedger) record(sessions []session, now int64) bool {
	if l.Codecs == nil {
		l.Codecs = map[string]*codecSeen{}
	}
	changed := false
	for _, current := range sessions {
		codec := learnable(current)
		if codec == "" {
			continue
		}
		client := current.client + " (" + current.app + ")"
		seen, ok := l.Codecs[codec]
		if !ok {
			seen = &codecSeen{FirstSeen: now}
			l.Codecs[codec] = seen
		}
		seen.LastSeen = now
		if !slices.Contains(seen.Clients, client) {
			seen.Clients = append(seen.Clients, client)
		}
		changed = true

		if current.file == "" {
			continue
		}
		l.Files = slices.DeleteFunc(l.Files, func(f fileSeen) bool { return f.Path == current.file })
		entry := fileSeen{Path: current.file, Codec: codec, App: current.app, Client: current.client, Seen: now}
		l.Files = append([]fileSeen{entry}, l.Files...)
		if len(l.Files) > ledgerFileLimit {
			l.Files = l.Files[:ledgerFileLimit]
		}
	}
	return changed
}

func ledgerLoad() {
	raw, err := os.ReadFile(ledgerFile)
	if err != nil {
		return
	}
	ledgerMu.Lock()
	defer ledgerMu.Unlock()
	if err := json.Unmarshal(raw, &ledger); err != nil {
		log.Printf("audio ledger: %v", err)
	}
}

func ledgerObserve(sessions []session, now int64) {
	ledgerMu.Lock()
	defer ledgerMu.Unlock()
	if !ledger.record(sessions, now) {
		return
	}
	raw, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return
	}
	// Renamed into place: audio-compat reads this from cron and must never see half a file.
	tmp := ledgerFile + ".tmp"
	if err := os.MkdirAll(filepath.Dir(ledgerFile), 0o755); err != nil {
		log.Printf("audio ledger: %v", err)
		return
	}
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		log.Printf("audio ledger: %v", err)
		return
	}
	if err := os.Rename(tmp, ledgerFile); err != nil {
		log.Printf("audio ledger: %v", err)
	}
}
