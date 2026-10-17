package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `#EXTM3U url-tvg="https://example.com/guide.xml" refresh="3600"
#EXTINF:-1 tvg-id="DAZN LaLiga HD" tvg-logo="https://x/l.png" group-title="LA LIGA",DAZN LaLiga 1080p *
acestream://cc108ae39f92c48f6c946763047bd1c9b7b7d889
#EXTINF:-1 tvg-id="DAZN LaLiga HD" tvg-logo="https://x/l.png" group-title="LA LIGA",DAZN LaLiga 1080p **
acestream://7f9857c45764ef2c7cdfb619b89db3056a57a51f
#EXTINF:-1 tvg-id="M+ LaLiga HD" tvg-logo="https://x/m.png" group-title="LA LIGA",M+ LaLiga 720p *
acestream://3ace1463a7cad979c3a4002b245487e9982e9f1a
#EXTINF:-1 tvg-id="DAZN LaLiga HD" tvg-logo="https://x/l.png" group-title="LA LIGA",DAZN LaLiga 1080p **
acestream://9c588ac92554746c489bc553773d959f6d2dfd8e
`

func build(t *testing.T, list string) (string, map[string]*channelGroup) {
	t.Helper()
	sources, m3u := parseAndDedup([]byte(list), "http://aceserve:6878/ace/getstream?id=", "http://p/ace/getstream?id=")
	order, groups := groupChannels(sources)
	return stableM3U(strings.SplitN(m3u, "\n", 2)[0], "http://acestream-proxy:6879/channel/", order, groups), groups
}

func TestSourcesFoldIntoChannels(t *testing.T) {
	m3u, groups := build(t, sample)

	dazn := groups["dazn-laliga-1080p"]
	if dazn == nil || len(dazn.IDs) != 3 || dazn.IDs[0] != "cc108ae39f92c48f6c946763047bd1c9b7b7d889" {
		t.Fatalf("dazn group = %+v, want its three ids in list order", dazn)
	}
	if groups["m-plus-laliga-720p"] == nil {
		t.Errorf("groups = %v, want M+ kept apart as m-plus-laliga-720p", groups)
	}
	if !strings.Contains(m3u, `group-title="LA LIGA",DAZN LaLiga 1080p`+"\nhttp://acestream-proxy:6879/channel/dazn-laliga-1080p\n") {
		t.Errorf("m3u lost the tags or the stable URL:\n%s", m3u)
	}
	if strings.Count(m3u, "#EXTINF") != 2 {
		t.Errorf("m3u has %d entries, want one per channel:\n%s", strings.Count(m3u, "#EXTINF"), m3u)
	}
	if !strings.HasPrefix(m3u, `#EXTM3U url-tvg="https://example.com/guide.xml"`) {
		t.Errorf("guide header lost: %q", strings.SplitN(m3u, "\n", 2)[0])
	}
}

// The whole point: new ids for the same channels must leave Jellyfin's playlist byte for byte.
func TestRotatedIDsLeaveThePlaylistAlone(t *testing.T) {
	before, _ := build(t, sample)
	rotated := strings.NewReplacer(
		"cc108ae39f92c48f6c946763047bd1c9b7b7d889", "1111111111111111111111111111111111111111",
		"7f9857c45764ef2c7cdfb619b89db3056a57a51f", "2222222222222222222222222222222222222222",
	).Replace(sample)
	after, groups := build(t, rotated)
	if before != after {
		t.Errorf("playlist changed on a rotation:\n%s\n---\n%s", before, after)
	}
	if groups["dazn-laliga-1080p"].IDs[0] != "1111111111111111111111111111111111111111" {
		t.Errorf("table kept the old id: %v", groups["dazn-laliga-1080p"].IDs)
	}
}

// A dropped id must vanish from the table on the next pass, not linger.
func TestTableIsReplacedWhole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channels.json")
	_, groups := build(t, sample)
	if changed, err := writeTable(path, groups); err != nil || !changed {
		t.Fatalf("first write: changed=%v err=%v", changed, err)
	}
	if changed, _ := writeTable(path, groups); changed {
		t.Error("an identical table was rewritten")
	}

	_, groups = build(t, strings.Replace(sample, "acestream://9c588ac92554746c489bc553773d959f6d2dfd8e\n", "acestream://\n", 1))
	if _, err := writeTable(path, groups); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var table channelTable
	if err := json.Unmarshal(data, &table); err != nil {
		t.Fatal(err)
	}
	for _, id := range table.Channels["dazn-laliga-1080p"].IDs {
		if id == "9c588ac92554746c489bc553773d959f6d2dfd8e" {
			t.Error("the dropped id is still in the table")
		}
	}
}
