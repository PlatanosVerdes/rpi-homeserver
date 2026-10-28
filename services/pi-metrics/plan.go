package main

import (
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
)

// The conversion plan for each file in the ledger, worked out here so that services/audio-convert
// only has to show it and run it. Every rule mirrors scripts/sync/audio-compat.py, which is what
// does the conversion: the same codec names, the same incompatible set, the same track choice. When
// the two disagree the script wins, since it reads the file itself.

const (
	targetCodec       = "ac3"
	targetBitrateKbps = 640
	targetMaxChannels = 6
)

var (
	// Converted before anything was measured, as audio-compat's SEED.
	seedCodecs = []string{"dts", "truehd"}
	// Each server keeps its own name for a codec, ffprobe has its own, and Plex says dca for DTS.
	ffprobeNames = map[string]string{"dca": "dts"}
	libraries    = []string{"films", "series"}
)

type track struct {
	Position int    `json:"position"` // among the audio streams, what ffmpeg's 0:a:N means
	Index    int    `json:"index"`    // among all streams
	Codec    string `json:"codec"`
	Language string `json:"language,omitempty"`
	Channels int    `json:"channels,omitempty"`
	Default  bool   `json:"default,omitempty"`
	Comment  bool   `json:"comment,omitempty"`
	Played   bool   `json:"played,omitempty"` // the track the session was playing, when it said
	Title    string `json:"title,omitempty"`  // the server's own description of it
}

// Why a file is listed but cannot be fixed, for a page that words it in its own language.
const (
	reasonDirectPlayOff = "direct_play_off"
	reasonRefusesTarget = "refuses_target"
	reasonHasPlayable   = "has_playable"
	reasonMainPlayable  = "main_playable"
	reasonNotLibrary    = "not_library"
)

type plan struct {
	Source        track   `json:"source"`  // the first of Sources, for readers that predate it
	Sources       []track `json:"sources"` // one per language the TV cannot play, each gets its own track
	TargetCodec   string  `json:"target_codec"`
	TargetBitrate int     `json:"target_bitrate_kbps"`
	TargetChannel int     `json:"target_channels"`
	ExtraBytes    int64   `json:"extra_bytes"`
	Summary       string  `json:"summary"`
}

// mediaInfo is what a server says about the file a session plays.
type mediaInfo struct {
	file     string
	title    string
	duration float64 // seconds
	tracks   []track
}

func ffprobeName(codec string) string {
	base := strings.Split(strings.ToLower(codec), "-")[0]
	if name, ok := ffprobeNames[base]; ok {
		return name
	}
	return base
}

// libraryPath is a file's path from its library folder down, as audio-compat's library_path.
func libraryPath(reported string) string {
	parts := strings.Split(path.Clean(reported), "/")
	for position, part := range parts {
		if slices.Contains(libraries, part) {
			return strings.Join(parts[position:], "/")
		}
	}
	return ""
}

func incompatibleCodecs(codecs map[string]*codecSeen) map[string]bool {
	out := map[string]bool{}
	for _, codec := range seedCodecs {
		out[codec] = true
	}
	for codec := range codecs {
		out[ffprobeName(codec)] = true
	}
	delete(out, targetCodec)
	return out
}

// sortTracks numbers the audio tracks in stream order, which is what ffmpeg's 0:a:N counts.
func sortTracks(tracks []track) []track {
	sort.SliceStable(tracks, func(i, j int) bool { return tracks[i].Index < tracks[j].Index })
	for position := range tracks {
		tracks[position].Position = position
		tracks[position].Codec = ffprobeName(tracks[position].Codec)
	}
	return tracks
}

// mainTrack is the one the client had re-encoded when the session said which, and otherwise
// audio-compat's default_audio_index: the track flagged default, else the first.
func mainTrack(tracks []track) track {
	for _, candidate := range tracks {
		if candidate.Played {
			return candidate
		}
	}
	for _, candidate := range tracks {
		if candidate.Default {
			return candidate
		}
	}
	return tracks[0]
}

func languageKey(t track) string {
	if t.Language == "" {
		return "und"
	}
	return strings.ToLower(t.Language)
}

// mainTracks is the main track of each language, the main track's language first, as audio-compat's
// main_tracks.
func mainTracks(tracks []track) []track {
	lead := mainTrack(tracks)
	mains := []track{lead}
	seen := map[string]bool{languageKey(lead): true}
	for _, t := range tracks {
		if !t.Comment && !seen[languageKey(t)] {
			seen[languageKey(t)] = true
			mains = append(mains, t)
		}
	}
	return mains
}

func sameLanguage(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	return a == b || a == "" || b == "" || a == "und" || b == "und"
}

// standIn is a compatible track that would play in the main track's place: same language, as many
// channels up to 5.1, not a commentary. audio-compat's stand_in is the same rule; change both.
func standIn(tracks []track, main track, incompatible map[string]bool) (track, bool) {
	wanted := main.Channels
	if wanted == 0 || wanted > targetMaxChannels {
		wanted = targetMaxChannels
	}
	for _, t := range tracks {
		if t.Index == main.Index || incompatible[t.Codec] || t.Comment || !sameLanguage(t.Language, main.Language) {
			continue
		}
		if t.Channels >= wanted {
			return t, true
		}
	}
	return track{}, false
}

func channelWords(channels int) string {
	switch channels {
	case 1:
		return "mono"
	case 2:
		return "stereo"
	case 6:
		return "5.1"
	case 8:
		return "7.1"
	}
	return fmt.Sprintf("%d channels", channels)
}

func describe(t track) string {
	if t.Title != "" {
		return t.Title
	}
	words := []string{}
	if t.Language != "" {
		words = append(words, t.Language)
	}
	words = append(words, strings.ToUpper(t.Codec))
	if t.Channels > 0 {
		words = append(words, channelWords(t.Channels))
	}
	return strings.Join(words, " ")
}

// decide fills in whether a file can be fixed by adding a track, and how.
func (f *fileSeen) decide(incompatible map[string]bool) {
	f.Actionable, f.Reason, f.ReasonCode, f.ReasonDetail, f.Plan = false, "", "", "", nil
	if f.Cause == "" {
		f.Cause = causeCodec // written before causes were recorded, when only codec was
	}
	if f.Library == "" {
		f.Library = libraryPath(f.Path)
	}
	switch {
	case f.Cause == causeNoDirectPlay:
		f.ReasonCode = reasonDirectPlayOff
		f.Reason = "the client turned direct play off; enable Force Direct Play in the client, a file change will not help"
		return
	case ffprobeName(f.Codec) == targetCodec:
		f.ReasonCode = reasonRefusesTarget
		f.Reason = "the client refuses AC3, which is the codec this would add; a file change will not help"
		return
	case f.Library == "":
		f.ReasonCode = reasonNotLibrary
		f.Reason = "not a file in the films or series library"
		return
	}
	if len(f.Tracks) == 0 {
		f.Actionable = true
		return
	}
	var sources []track
	for position, main := range mainTracks(f.Tracks) {
		other, playable := standIn(f.Tracks, main, incompatible)
		switch {
		case incompatible[main.Codec] && !playable:
			sources = append(sources, main)
		case position > 0:
		case !incompatible[main.Codec]:
			f.ReasonCode, f.ReasonDetail = reasonMainPlayable, describe(main)
			f.Reason = fmt.Sprintf("its main track is %s, a codec no client has been refused", describe(main))
		default:
			f.ReasonCode, f.ReasonDetail = reasonHasPlayable, describe(other)
			f.Reason = fmt.Sprintf("it already has %s, which plays in place of the %s track; pick it in the player",
				describe(other), describe(main))
		}
	}
	if len(sources) == 0 {
		return
	}
	f.Actionable, f.Reason, f.ReasonCode, f.ReasonDetail = true, "", "", ""

	channels := sources[0].Channels
	if channels == 0 || channels > targetMaxChannels {
		channels = targetMaxChannels
	}
	names := make([]string, len(sources))
	for i, source := range sources {
		names[i] = describe(source)
	}
	added := fmt.Sprintf("%s %s", strings.ToUpper(targetCodec), channelWords(channels))
	if len(sources) > 1 {
		added = fmt.Sprintf("%d %s tracks", len(sources), strings.ToUpper(targetCodec))
	}
	f.Plan = &plan{
		Source:        sources[0],
		Sources:       sources,
		TargetCodec:   targetCodec,
		TargetBitrate: targetBitrateKbps,
		TargetChannel: channels,
		ExtraBytes:    int64(float64(targetBitrateKbps*1000/8)*f.Duration) * int64(len(sources)),
		Summary:       fmt.Sprintf("add %s from the %s track; every track kept", added, strings.Join(names, " and ")),
	}
}
