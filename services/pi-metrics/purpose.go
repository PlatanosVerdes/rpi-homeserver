package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Where the disk went, by *why* those bytes are there rather than by folder. The folder answer is
// misleading on this box: an import is a hardlink, so the same film is counted under films/ and
// under downloads/ and neither number is the disk it costs.
//
// One inode, one purpose:
//
//	watched_kept    in the library and still seeding. The bytes are shared, so the tracker is being
//	                paid for free
//	watched_only    in the library with no torrent left. Yours, costing what it costs
//	seed_debt       gone from the library, torrent still here: bytes kept only to finish paying a
//	                tracker for something already watched
//	ratio           autobrr's grabs. Nothing ever asked for these; they exist to build ratio
//	cross_seed      a cross-seed that is the last link to its bytes
//	unclaimed       in downloads and no torrent claims it
//	other           backups, databases, anything outside the media folders
//
// The purposes add up to the used space on the data disk, which is what makes them worth a pie.
func purpose(seen map[uint64]biggest) []string {
	byTag := torrentTags()
	totals := map[string]int64{}

	for _, file := range seen {
		inDownloads := file.roots["downloads"]
		inLibrary := file.roots["films"] || file.roots["series"] || file.roots["tv"]
		switch {
		case inLibrary && inDownloads:
			totals["watched_kept"] += file.size
		case inLibrary:
			totals["watched_only"] += file.size
		case inDownloads:
			totals[downloadPurpose(file.path, byTag)] += file.size
		default:
			totals["other"] += file.size
		}
	}

	lines := []string{
		"# HELP disk_purpose_bytes Disk on the data drive by why those bytes are there, one inode counted once",
		"# TYPE disk_purpose_bytes gauge",
	}
	for _, name := range sorted(totals) {
		lines = append(lines, fmt.Sprintf("disk_purpose_bytes{purpose=%q} %d", name, totals[name]))
	}
	return lines
}

// The tags of every torrent, keyed by the first path component under downloads/, which is how a file
// on disk is traced back to the torrent that holds it.
func torrentTags() map[string]map[string]bool {
	out := map[string]map[string]bool{}
	items, err := qbitTorrents()
	if err != nil {
		return out
	}
	downloads := filepath.Join(dataRoot, "downloads")
	for _, torrent := range items {
		content := str(torrent, "content_path")
		if content == "" {
			continue
		}
		local := strings.Replace(content, "/data/", dataRoot+"/", 1)
		relative, err := filepath.Rel(downloads, local)
		if err != nil || strings.HasPrefix(relative, "..") {
			continue
		}
		key := strings.SplitN(relative, string(filepath.Separator), 2)[0]
		tags := map[string]bool{}
		for _, tag := range strings.Split(str(torrent, "tags"), ",") {
			if trimmed := strings.TrimSpace(tag); trimmed != "" {
				tags[trimmed] = true
			}
		}
		if existing, ok := out[key]; ok {
			// Two torrents on the same files, which is what a cross-seed is: keep both sets.
			for tag := range tags {
				existing[tag] = true
			}
			continue
		}
		out[key] = tags
	}
	return out
}

func downloadPurpose(path string, byTag map[string]map[string]bool) string {
	relative, err := filepath.Rel(filepath.Join(dataRoot, "downloads"), path)
	if err != nil {
		return "unclaimed"
	}
	tags, known := byTag[strings.SplitN(relative, string(filepath.Separator), 2)[0]]
	switch {
	case !known:
		return "unclaimed"
	case tags["ratio"]:
		return "ratio"
	case tags["noHL"]:
		return "seed_debt"
	case tags["cross-seed"]:
		return "cross_seed"
	}
	// A torrent whose file is not in the library and carries none of those tags: the arr imported
	// it by copy rather than by hardlink, or somebody added it by hand.
	return "seed_debt"
}
