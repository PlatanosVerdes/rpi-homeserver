package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// What each media server is re-encoding right now, and for whom.
//
// Three labels carry the whole decision, and each one exists because what it separates is a
// different problem: `stream`, because video is what costs this Pi its CPU and audio is not;
// `browser`, because a browser's audio re-encode is the one kind with no fix on this end; and
// `codec`, so the message names what to go and change. The table behind all three, and the month of
// sessions it was measured from, is in docs/alerting.md.
//
// A remux is not a transcode and is not counted as one, or the acestream Live TV channels would
// report several a day for something free. Jellyfin says which it is with IsVideoDirect and
// IsAudioDirect, both of which stay true through a remux; Plex with a container-only decision.

const (
	streamVideo = "video"
	streamAudio = "audio"
)

// A browser is recognised from the product name first (Plex Web, Jellyfin Web) and from the platform
// second, because a browser reports itself either way depending on the server.
var browserPlatforms = map[string]bool{
	"chrome": true, "chromium": true, "firefox": true, "safari": true, "edge": true, "opera": true,
}

type session struct {
	app     string
	client  string
	browser bool
	method  string            // directplay, directstream or transcode
	recodes map[string]string // video/audio -> the source codec being re-encoded
}

func isBrowser(product, platform string) bool {
	if strings.Contains(strings.ToLower(product), "web") {
		return true
	}
	return browserPlatforms[strings.ToLower(platform)]
}

// fetch is getJSON with arbitrary headers: Plex and Jellyfin each authenticate their own way and
// neither takes the X-Api-Key the arrs use.
func fetch(url string, headers map[string]string, into any) error {
	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	resp, err := client.Do(request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(into)
}

func plexSessions() ([]session, error) {
	var answer struct {
		MediaContainer struct {
			Metadata []map[string]any `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	headers := map[string]string{"Accept": "application/json", "X-Plex-Token": plexToken}
	if err := fetch(plexURL+"/status/sessions", headers, &answer); err != nil {
		return nil, fmt.Errorf("plex: %w", err)
	}

	out := make([]session, 0, len(answer.MediaContainer.Metadata))
	for _, row := range answer.MediaContainer.Metadata {
		player := obj(row, "Player")
		current := session{
			app:     "plex",
			client:  orQ(str(player, "title")),
			browser: isBrowser(str(player, "product"), str(player, "platform")),
			method:  "directplay",
			recodes: map[string]string{},
		}

		// No TranscodeSession at all is a direct play. With one, its two decisions say which stream
		// is being re-encoded and which is only being copied into another container.
		if transcode, ok := row["TranscodeSession"].(map[string]any); ok {
			current.method = "directstream"
			if str(transcode, "videoDecision") == "transcode" {
				current.method = "transcode"
				current.recodes[streamVideo] = orQ(str(transcode, "sourceVideoCodec"))
			}
			if str(transcode, "audioDecision") == "transcode" {
				current.method = "transcode"
				current.recodes[streamAudio] = orQ(str(transcode, "sourceAudioCodec"))
			}
		}
		out = append(out, current)
	}
	return out, nil
}

func jellyfinSessions() ([]session, error) {
	var rows []map[string]any
	headers := map[string]string{"Authorization": "MediaBrowser Token=" + jellyfinKey}
	if err := fetch(jellyfinURL+"/Sessions", headers, &rows); err != nil {
		return nil, fmt.Errorf("jellyfin: %w", err)
	}

	out := make([]session, 0, len(rows))
	for _, row := range rows {
		playing, ok := row["NowPlayingItem"].(map[string]any)
		if !ok {
			continue // connected but idle, which is most of them
		}
		state := obj(row, "PlayState")
		current := session{
			app:     "jellyfin",
			client:  orQ(str(row, "DeviceName")),
			browser: isBrowser(str(row, "Client"), str(row, "DeviceName")),
			method:  "directplay",
			recodes: map[string]string{},
		}

		// IsVideoDirect/IsAudioDirect are what separate a remux from a re-encode: TranscodingInfo is
		// present for both, and both flags true means every stream is being copied.
		if info, ok := row["TranscodingInfo"].(map[string]any); ok {
			current.method = "directstream"
			if !truthy(info, "IsVideoDirect") {
				current.method = "transcode"
				current.recodes[streamVideo] = sourceCodec(playing, state, "Video")
			}
			if !truthy(info, "IsAudioDirect") {
				current.method = "transcode"
				current.recodes[streamAudio] = sourceCodec(playing, state, "Audio")
			}
		}
		out = append(out, current)
	}
	return out, nil
}

// sourceCodec is the codec of the track being played, not the one being produced. TranscodingInfo
// carries the target ("the aac it is making"), and the actionable half is the source ("the dca it
// cannot send"). Which track that is comes from the session, since a file usually has several.
func sourceCodec(playing, state map[string]any, kind string) string {
	streams := objs(playing, "MediaStreams")
	wanted, pick := "AudioStreamIndex", -1
	if kind == "Video" {
		wanted = "VideoStreamIndex"
	}
	if index, ok := state[wanted].(float64); ok {
		pick = int(index)
	}
	var fallback string
	for _, stream := range streams {
		if str(stream, "Type") != kind {
			continue
		}
		if pick >= 0 && int(num(stream, "Index")) == pick {
			return orQ(str(stream, "Codec"))
		}
		if fallback == "" || truthy(stream, "IsDefault") {
			fallback = str(stream, "Codec")
		}
	}
	return orQ(fallback)
}

func playback() ([]string, []string) {
	var problems []string
	var found []session

	// An app with no credentials is not configured here, which is not a failure: the acestream
	// profile runs Jellyfin without Plex, and a Plex-only install is just as valid.
	if plexToken != "" {
		rows, err := plexSessions()
		if err != nil {
			problems = append(problems, err.Error())
		} else {
			found = append(found, rows...)
		}
	}
	if jellyfinKey != "" {
		rows, err := jellyfinSessions()
		if err != nil {
			problems = append(problems, err.Error())
		} else {
			found = append(found, rows...)
		}
	}
	if len(problems) > 0 && len(found) == 0 {
		return nil, problems
	}

	// Every method is published for every configured app, zero included, so a panel reads as "nobody
	// is watching" instead of going blank. A re-encode cannot be published that way: its labels only
	// exist while the session does, so those series appear and disappear with it.
	counts := map[string]int{}
	for _, app := range configuredApps() {
		for _, method := range []string{"directplay", "directstream", "transcode"} {
			counts[app+"\x00"+method] = 0
		}
	}
	recodes := map[string]int{}
	for _, current := range found {
		counts[current.app+"\x00"+current.method]++
		for kind, codec := range current.recodes {
			key := strings.Join([]string{current.app, kind, codec, current.client, fmt.Sprint(current.browser)}, "\x00")
			recodes[key]++
		}
	}

	lines := []string{
		"# HELP media_playback_sessions Playback sessions by how the server is delivering them",
		"# TYPE media_playback_sessions gauge",
	}
	for _, key := range sorted(counts) {
		parts := strings.Split(key, "\x00")
		lines = append(lines, fmt.Sprintf("media_playback_sessions{app=%q,method=%q} %d", parts[0], parts[1], counts[key]))
	}

	lines = append(lines,
		"# HELP media_transcode_sessions Sessions whose video or audio is being re-encoded, by the source codec that forced it",
		"# TYPE media_transcode_sessions gauge")
	for _, key := range sorted(recodes) {
		parts := strings.Split(key, "\x00")
		lines = append(lines, fmt.Sprintf(
			"media_transcode_sessions{app=%q,stream=%q,codec=%q,client=%q,browser=%q} %d",
			parts[0], parts[1], parts[2], parts[3], parts[4], recodes[key]))
	}
	return lines, problems
}

func configuredApps() []string {
	var out []string
	if plexToken != "" {
		out = append(out, "plex")
	}
	if jellyfinKey != "" {
		out = append(out, "jellyfin")
	}
	return out
}
