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
// A session only counts when the server says the codec itself was refused (cause.go), its client
// is not a browser, and its video was copied. A re-encode alone proves nothing: a Samsung TV that
// turned direct play off had EAC3 re-encoded, then the AC3 added for it, because that fallback
// takes only AAC. A browser re-encodes every surround codec and nothing on this end changes that.
// When the video is re-encoded too, the audio goes along whatever its codec: Jellyfin's Live TV
// re-encodes aac that way every day, and learning from it would mark the codec every client plays.
//
// The file list also keeps sessions whose client turned direct play off. They teach no codec and
// are marked not actionable, so services/audio-convert can say why converting would not help.
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

	// What services/audio-convert lists: the cause, what the server says about the file, and
	// whether adding a track can fix it (plan.go). audio-compat reads only the fields above, and
	// only from entries whose cause is codec.
	Cause        string  `json:"cause"`
	Title        string  `json:"title,omitempty"`
	Duration     float64 `json:"duration,omitempty"`
	Library      string  `json:"library_path,omitempty"`
	Tracks       []track `json:"tracks,omitempty"`
	Actionable   bool    `json:"actionable"`
	Reason       string  `json:"reason,omitempty"`
	ReasonCode   string  `json:"reason_code,omitempty"`
	ReasonDetail string  `json:"reason_detail,omitempty"`
	Plan         *plan   `json:"plan,omitempty"`
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
	if current.browser || current.cause != causeCodec {
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

// listable is the source audio codec of a session whose file belongs in the ledger, or "". That
// is every learnable session, plus one whose client turned direct play off: the app lists those
// too, greyed, so the reason is on the page and not only in an alert.
func listable(current session) string {
	if codec := learnable(current); codec != "" {
		return codec
	}
	if current.cause != causeNoDirectPlay {
		return ""
	}
	current.cause = causeCodec
	return learnable(current)
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
			if l.recordFile(current, listable(current), now) {
				changed = true
			}
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
		l.recordFile(current, codec, now)
	}
	if changed {
		incompatible := incompatibleCodecs(l.Codecs)
		for position := range l.Files {
			l.Files[position].decide(incompatible)
		}
	}
	return changed
}

// recordFile puts the session's file at the head of the queue. A codec entry is never replaced by
// a no_direct_play one: the file still needs the track, whatever another client did with it since.
func (l *audioLedger) recordFile(current session, codec string, now int64) bool {
	if codec == "" || current.file == "" {
		return false
	}
	if current.cause != causeCodec && slices.ContainsFunc(l.Files, func(f fileSeen) bool {
		return f.Path == current.file && f.Cause != causeNoDirectPlay
	}) {
		return false
	}
	l.Files = slices.DeleteFunc(l.Files, func(f fileSeen) bool { return f.Path == current.file })
	entry := fileSeen{
		Path: current.file, Codec: codec, App: current.app, Client: current.client, Seen: now,
		Cause: current.cause, Title: current.title, Duration: current.duration,
		Library: libraryPath(current.file), Tracks: current.tracks,
	}
	l.Files = append([]fileSeen{entry}, l.Files...)
	if len(l.Files) > ledgerFileLimit {
		l.Files = l.Files[:ledgerFileLimit]
	}
	return true
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
	incompatible := incompatibleCodecs(ledger.Codecs)
	for position := range ledger.Files {
		ledger.Files[position].decide(incompatible)
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
