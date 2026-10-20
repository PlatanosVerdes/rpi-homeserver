package main

import (
	"strings"
	"testing"
)

func tracks(list ...track) []track { return sortTracks(list) }

func withMedia(s session, duration float64, list ...track) session {
	s.title, s.duration, s.tracks = "A film (2020)", duration, tracks(list...)
	return s
}

func onlyFile(t *testing.T, sessions ...session) fileSeen {
	t.Helper()
	var l audioLedger
	l.record(sessions, 100)
	if len(l.Files) != 1 {
		t.Fatalf("want one file, got %+v", l.Files)
	}
	return l.Files[0]
}

func TestPlanBuildsFromTheDefaultTrack(t *testing.T) {
	f := onlyFile(t, withMedia(audioOnly("TV 2025", "dca", "/data/films/A (2020)/a.mkv"), 7200,
		track{Index: 2, Codec: "dca", Language: "Spanish", Channels: 6, Title: "Spanish (DTS 5.1)"},
		track{Index: 1, Codec: "dca", Language: "English", Channels: 8, Default: true, Title: "English (DTS-HD MA 7.1)"}))

	if !f.Actionable || f.Plan == nil {
		t.Fatalf("not actionable: %+v", f)
	}
	p := f.Plan
	if p.Source.Index != 1 || p.Source.Position != 0 || p.Source.Codec != "dts" || !p.Source.Default {
		t.Fatalf("wrong source: %+v", p.Source)
	}
	if p.TargetCodec != "ac3" || p.TargetBitrate != 640 || p.TargetChannel != 6 {
		t.Fatalf("wrong target: %+v", p)
	}
	if p.ExtraBytes != 80_000*7200 {
		t.Fatalf("extra bytes %d", p.ExtraBytes)
	}
	if p.Summary != "add AC3 5.1 from the English (DTS-HD MA 7.1) track; every track kept" {
		t.Fatalf("summary %q", p.Summary)
	}
	if f.Library != "films/A (2020)/a.mkv" || f.Title != "A film (2020)" {
		t.Fatalf("file fields: %+v", f)
	}
}

func TestPlanWithoutADefaultTakesTheFirstTrack(t *testing.T) {
	f := onlyFile(t, withMedia(audioOnly("TV", "truehd", "/data/series/S/e.mkv"), 60,
		track{Index: 3, Codec: "truehd", Channels: 2},
		track{Index: 1, Codec: "truehd", Channels: 8}))
	if f.Plan == nil || f.Plan.Source.Index != 1 || f.Plan.TargetChannel != 6 {
		t.Fatalf("plan %+v", f.Plan)
	}

	stereo := onlyFile(t, withMedia(audioOnly("TV", "truehd", "/data/series/S/e.mkv"), 60,
		track{Index: 1, Codec: "truehd", Channels: 2, Language: "jpn"}))
	if stereo.Plan.TargetChannel != 2 || stereo.Plan.Summary != "add AC3 stereo from the jpn TRUEHD stereo track; every track kept" {
		t.Fatalf("stereo plan %+v", stereo.Plan)
	}
}

func TestPlanRefusesWhatATrackCannotFix(t *testing.T) {
	cases := []struct {
		name    string
		session session
		reason  string
	}{
		{"ac3 refused", withMedia(audioOnly("TV", "ac3", "/data/films/a.mkv"), 60,
			track{Index: 1, Codec: "ac3", Channels: 6}), "refuses AC3"},
		{"direct play off", withMedia(withCause(audioOnly("TV", "eac3", "/data/films/b.mkv"), causeNoDirectPlay), 60,
			track{Index: 1, Codec: "eac3", Channels: 6}), "Force Direct Play"},
		{"direct play off on ac3", withCause(audioOnly("TV", "ac3", "/data/films/c.mkv"), causeNoDirectPlay), "Force Direct Play"},
		{"already has a playable track", withMedia(audioOnly("TV", "dca", "/data/films/d.mkv"), 60,
			track{Index: 1, Codec: "dca", Default: true},
			track{Index: 2, Codec: "aac", Title: "English (AAC Stereo)"}), "English (AAC Stereo)"},
		{"not a library file", audioOnly("TV", "dca", "/media/elsewhere/e.mkv"), "library"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := onlyFile(t, c.session)
			if f.Actionable || f.Plan != nil || !strings.Contains(f.Reason, c.reason) {
				t.Fatalf("want non-actionable with %q, got %+v", c.reason, f)
			}
		})
	}
}

func TestDirectPlayOffTeachesNothingAndDoesNotHideACodecEntry(t *testing.T) {
	var l audioLedger
	off := withCause(audioOnly("TV", "eac3", "/data/films/a.mkv"), causeNoDirectPlay)
	l.record([]session{off}, 100)
	if len(l.Codecs) != 0 || len(l.Files) != 1 || l.Files[0].Cause != causeNoDirectPlay {
		t.Fatalf("after direct play off: %+v", l)
	}

	l.record([]session{audioOnly("TV", "eac3", "/data/films/a.mkv")}, 200)
	if len(l.Files) != 1 || l.Files[0].Cause != causeCodec || !l.Files[0].Actionable {
		t.Fatalf("codec entry did not replace it: %+v", l.Files)
	}

	l.record([]session{off}, 300)
	if len(l.Files) != 1 || l.Files[0].Cause != causeCodec || l.Files[0].Seen != 200 {
		t.Fatalf("direct play off replaced a codec entry: %+v", l.Files)
	}
}

func TestIgnoredSessionsAreNotListed(t *testing.T) {
	for name, s := range map[string]session{
		"browser": withCause(session{app: "plex", client: "Chrome", browser: true,
			recodes: map[string]string{streamAudio: "eac3"}, file: "/data/films/c.mkv"}, causeNoDirectPlay),
		"video re-encoded": withCause(session{app: "plex", client: "TV",
			recodes: map[string]string{streamVideo: "hevc", streamAudio: "eac3"}, file: "/data/films/c.mkv"}, causeNoDirectPlay),
		"other reason": withCause(audioOnly("TV", "eac3", "/data/films/h.mkv"), causeOther),
		"unknown":      withCause(audioOnly("TV", "eac3", "/data/films/i.mkv"), causeUnknown),
	} {
		var l audioLedger
		if l.record([]session{s}, 100) || len(l.Files) != 0 {
			t.Errorf("%s: listed %+v", name, l.Files)
		}
	}
}

func TestLearningACodecMakesEarlierFilesActionable(t *testing.T) {
	var l audioLedger
	l.record([]session{withMedia(audioOnly("TV", "dca", "/data/films/a.mkv"), 60,
		track{Index: 1, Codec: "dca", Default: true}, track{Index: 2, Codec: "eac3"})}, 100)
	if l.Files[0].Actionable {
		t.Fatalf("eac3 not refused yet, should not be actionable: %+v", l.Files[0])
	}
	l.record([]session{audioOnly("TV", "eac3", "/data/films/b.mkv")}, 200)
	if !l.Files[1].Actionable {
		t.Fatalf("eac3 refused now, a.mkv should be actionable: %+v", l.Files[1])
	}
}

func TestOldLedgerEntriesAreReadAsCodec(t *testing.T) {
	f := fileSeen{Path: "/data/films/a.mkv", Codec: "dts"}
	f.decide(incompatibleCodecs(nil))
	if f.Cause != causeCodec || !f.Actionable || f.Library != "films/a.mkv" {
		t.Fatalf("%+v", f)
	}
}
