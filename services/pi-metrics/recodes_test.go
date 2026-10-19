package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func audioOnly(client, codec, file string) session {
	return session{app: "plex", client: client, method: "transcode", cause: causeCodec,
		recodes: map[string]string{streamAudio: codec}, file: file}
}

func withCause(s session, cause string) session {
	s.cause = cause
	return s
}

func TestRecordLearnsOnlyWhatTheClientCouldNotPlay(t *testing.T) {
	cases := []struct {
		name    string
		session session
		learned string
	}{
		{"tv, video copied", audioOnly("TV 2025", "eac3", "/data/films/a.mkv"), "eac3"},
		{"codec case is the server's", audioOnly("TV 2025", "DCA", "/data/films/b.mkv"), "dca"},
		{"browser", session{app: "plex", client: "Chrome", browser: true,
			recodes: map[string]string{streamAudio: "eac3"}, file: "/data/films/c.mkv"}, ""},
		{"video re-encoded too", session{app: "jellyfin", client: "Samsung Smart TV",
			recodes: map[string]string{streamVideo: "h264", streamAudio: "aac"}, file: "http://bifrost/x"}, ""},
		{"video only", session{app: "plex", client: "TV 2025",
			recodes: map[string]string{streamVideo: "hevc"}, file: "/data/films/d.mkv"}, ""},
		{"codec unknown", audioOnly("TV 2025", "?", "/data/films/e.mkv"), ""},
		{"client turned direct play off", withCause(audioOnly("TV 2025", "ac3", "/data/films/g.mkv"), causeNoDirectPlay), ""},
		{"jellyfin, another reason", withCause(audioOnly("TV", "eac3", "/data/films/h.mkv"), causeOther), ""},
		{"cause unknown", withCause(audioOnly("TV 2025", "eac3", "/data/films/i.mkv"), causeUnknown), ""},
		{"direct stream", session{app: "plex", client: "TV 2025", method: "directstream",
			recodes: map[string]string{}, file: "/data/films/f.mkv"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var l audioLedger
			changed := l.record([]session{c.session}, 100)
			if c.learned == "" {
				if changed || len(l.Codecs) != 0 || len(l.Files) != 0 {
					t.Fatalf("learned something from a session that says nothing: %+v", l)
				}
				return
			}
			if !changed || l.Codecs[c.learned] == nil {
				t.Fatalf("did not learn %q: %+v", c.learned, l.Codecs)
			}
			if len(l.Files) != 1 || l.Files[0].Path != c.session.file || l.Files[0].Codec != c.learned {
				t.Fatalf("file not queued: %+v", l.Files)
			}
		})
	}
}

func TestRecordKeepsOneEntryPerFileNewestFirst(t *testing.T) {
	var l audioLedger
	l.record([]session{audioOnly("TV 2025", "eac3", "/data/films/a.mkv")}, 100)
	l.record([]session{audioOnly("Phone", "eac3", "/data/films/b.mkv")}, 200)
	l.record([]session{audioOnly("TV 2025", "eac3", "/data/films/a.mkv")}, 300)

	if len(l.Files) != 2 || l.Files[0].Path != "/data/films/a.mkv" || l.Files[0].Seen != 300 {
		t.Fatalf("files: %+v", l.Files)
	}
	seen := l.Codecs["eac3"]
	if seen.FirstSeen != 100 || seen.LastSeen != 300 || len(seen.Clients) != 2 {
		t.Fatalf("codec: %+v", seen)
	}

	for i := 0; i < ledgerFileLimit+10; i++ {
		l.record([]session{audioOnly("TV 2025", "eac3", fmt.Sprintf("/data/films/%d.mkv", i))}, int64(400+i))
	}
	if len(l.Files) != ledgerFileLimit {
		t.Fatalf("queue grew to %d", len(l.Files))
	}
}

func TestLedgerSurvivesARestart(t *testing.T) {
	ledgerFile = filepath.Join(t.TempDir(), "audio-recodes.json")
	ledger = audioLedger{}
	ledgerObserve([]session{audioOnly("TV 2025", "eac3", "/data/films/a.mkv")}, 100)

	ledger = audioLedger{}
	ledgerLoad()
	if ledger.Codecs["eac3"] == nil || len(ledger.Files) != 1 {
		t.Fatalf("lost on reload: %+v", ledger)
	}
}

func TestPlexAudioCauseReadsTheDecisionRequest(t *testing.T) {
	plexLogs = t.TempDir()
	plexCauses = map[string]string{}
	log := strings.Join([]string{
		`Oct 19, 2026 23:10:17.077 [1] DEBUG - Request: [192.168.1.128:34488 (Allowed Network (Subnet))] GET /video/:/transcode/universal/decision?directPlay=0&directStream=1&protocol=dash&session=offsession&mediaIndex=0`,
		`Oct 19, 2026 23:35:00.000 [1] DEBUG - Request: [192.168.1.128:1 (Allowed Network (Subnet))] GET /video/:/transcode/universal/decision?directPlay=1&directStream=1&protocol=http&session=onsession&mediaIndex=0`,
		`Oct 19, 2026 23:35:00.100 [1] DEBUG - Completed: [192.168.1.128:1] 200 GET /video/:/transcode/universal/decision?directPlay=0&session=onsession`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(plexLogs, plexLogFiles[0]), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{
		"offsession": causeNoDirectPlay, "onsession": causeCodec, "absent": causeUnknown, "": causeUnknown,
	} {
		if got := plexAudioCause(id); got != want {
			t.Errorf("%q: %s, want %s", id, got, want)
		}
	}
}

func TestJellyfinAudioCause(t *testing.T) {
	for _, c := range []struct {
		raw  any
		want string
	}{
		{[]any{"ContainerNotSupported", "AudioCodecNotSupported"}, causeCodec},
		{"ContainerNotSupported, AudioCodecNotSupported", causeCodec},
		{[]any{"DirectPlayError"}, causeOther},
		{nil, causeUnknown},
	} {
		if got := jellyfinAudioCause(c.raw); got != c.want {
			t.Errorf("%v: %s, want %s", c.raw, got, c.want)
		}
	}
}

func TestSessionsCarryCauseAndFile(t *testing.T) {
	plexLogs = t.TempDir()
	plexCauses = map[string]string{}
	request := "DEBUG - Request: [x] GET /video/:/transcode/universal/start.mpd?directPlay=1&session=abc&path=x\n"
	os.WriteFile(filepath.Join(plexLogs, plexLogFiles[0]), []byte(request), 0o644)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status/sessions":
			// What Plex really sends mid-transcode: Media describes the stream, and Part has no file.
			fmt.Fprint(w, `{"MediaContainer":{"Metadata":[{"ratingKey":"853",
				"Player":{"title":"TV 2025","product":"Plex for Samsung","platform":"Tizen"},
				"Media":[{"id":"2214","container":"mp4","audioCodec":"aac","Part":[{"id":"2214","decision":"transcode"}]}],
				"TranscodeSession":{"key":"/transcode/sessions/abc","videoDecision":"copy","audioDecision":"transcode","sourceAudioCodec":"eac3"}}]}}`)
		case "/library/metadata/853":
			fmt.Fprint(w, `{"MediaContainer":{"Metadata":[{"Media":[
				{"id":"1111","Part":[{"file":"/data/films/Other/b.mkv"}]},
				{"id":"2214","Part":[{"file":"/data/films/Spider-Man/a.mkv"}]}]}]}}`)
		case "/Sessions":
			fmt.Fprint(w, `[{"DeviceName":"Living room","Client":"Jellyfin Android TV",
				"NowPlayingItem":{"Path":"/data/series/b.mkv","MediaStreams":[{"Type":"Audio","Index":1,"Codec":"truehd"}]},
				"PlayState":{"AudioStreamIndex":1},
				"TranscodingInfo":{"IsVideoDirect":true,"IsAudioDirect":false,"TranscodeReasons":["AudioCodecNotSupported"]}}]`)
		}
	}))
	defer server.Close()
	plexURL, plexToken, jellyfinURL, jellyfinKey = server.URL, "x", server.URL, "x"

	plex, err := plexSessions()
	if err != nil || len(plex) != 1 || plex[0].cause != causeCodec ||
		plex[0].file != "/data/films/Spider-Man/a.mkv" || learnable(plex[0]) != "eac3" {
		t.Fatalf("plex: %v %+v", err, plex)
	}
	jellyfin, err := jellyfinSessions()
	if err != nil || len(jellyfin) != 1 || jellyfin[0].file != "/data/series/b.mkv" || learnable(jellyfin[0]) != "truehd" {
		t.Fatalf("jellyfin: %v %+v", err, jellyfin)
	}
}
