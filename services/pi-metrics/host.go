package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var dataRoot = env("DATA_ROOT", "/mnt/data")

// Which top-level folder a hardlinked file is charged to. Bytes shared between a download and its
// import belong to both, so somebody has to be charged, and `du` charged whichever it happened to
// walk first. The library goes first here on purpose: a panel that answers "where did the disk go"
// by showing films/ as small because its bytes were charged to downloads/ answers the wrong
// question. Either way the folders add up to the same disk.
var chargeFirst = []string{"films", "series"}

const topFiles = 30

// zram reads /sys/block/zram*/mm_stat, whose columns are orig_data_size compr_data_size
// mem_used_total mem_limit mem_used_max same_pages pages_compacted huge_pages.
//
// node_exporter reports swap the same way whether it lives on a disk or in compressed RAM, so its
// SWAP gauge sits red while nothing is wrong. The number it cannot give is mem_used_total: the real
// RAM those compressed pages occupy.
func zram() ([]string, []string) {
	lines := []string{
		"# HELP zram_original_bytes Uncompressed size of the data held in zram",
		"# TYPE zram_original_bytes gauge",
		"# HELP zram_compressed_bytes Size of that data after compression",
		"# TYPE zram_compressed_bytes gauge",
		"# HELP zram_memory_used_bytes Real RAM zram occupies, including its own overhead",
		"# TYPE zram_memory_used_bytes gauge",
		"# HELP zram_memory_used_max_bytes High-water mark of that real RAM since boot",
		"# TYPE zram_memory_used_max_bytes gauge",
		"# HELP zram_compression_ratio Original bytes per byte of real RAM used",
		"# TYPE zram_compression_ratio gauge",
	}
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return lines, []string{err.Error()}
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "zram") {
			continue
		}
		raw, err := os.ReadFile("/sys/block/" + entry.Name() + "/mm_stat")
		if err != nil {
			continue
		}
		fields := strings.Fields(string(raw))
		if len(fields) < 5 {
			continue
		}
		values := make([]int64, 5)
		for i := 0; i < 5; i++ {
			values[i], _ = strconv.ParseInt(fields[i], 10, 64)
		}
		original, compressed, used, usedMax := values[0], values[1], values[2], values[4]
		label := fmt.Sprintf("{device=%q}", entry.Name())
		ratio := 1.0
		if used > 0 {
			ratio = float64(original) / float64(used)
		}
		lines = append(lines,
			fmt.Sprintf("zram_original_bytes%s %d", label, original),
			fmt.Sprintf("zram_compressed_bytes%s %d", label, compressed),
			fmt.Sprintf("zram_memory_used_bytes%s %d", label, used),
			fmt.Sprintf("zram_memory_used_max_bytes%s %d", label, usedMax),
			// Derived here rather than on the panel: dividing by zero on an empty device would
			// render as NaN on a panel that exists to be reassuring.
			fmt.Sprintf("zram_compression_ratio%s %.3f", label, ratio),
		)
	}
	return lines, nil
}

type biggest struct {
	size int64
	path string
	// Which top-level folders this inode was found in. An import is a hardlink, so the same bytes
	// appear under films/ and under downloads/, and which folders hold it is what says whether
	// deleting the torrent would free anything.
	roots map[string]bool
}

// disk answers both questions in one walk, which is half the I/O the `find` and `du` it replaces
// cost: the largest files, and the total per top-level folder. An inode is counted once in both,
// because the arrs import by hardlink and the same bytes carry two names.
func disk() ([]string, []string) {
	seen := map[uint64]biggest{}
	perRoot := map[string]int64{}
	var problems []string

	tops, err := os.ReadDir(dataRoot)
	if err != nil {
		return nil, []string{err.Error()}
	}
	var order []string
	for _, name := range chargeFirst {
		for _, entry := range tops {
			if entry.IsDir() && entry.Name() == name {
				order = append(order, name)
			}
		}
	}
	for _, entry := range tops {
		if !entry.IsDir() {
			continue
		}
		perRoot[entry.Name()] = 0
		if !contains(chargeFirst, entry.Name()) {
			order = append(order, entry.Name())
		}
	}

	for _, top := range order {
		root := top
		filepath.WalkDir(filepath.Join(dataRoot, top), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return nil //nolint:nilerr // an unreadable corner of the disk is not worth failing over
			}
			info, err := entry.Info()
			if err != nil {
				return nil
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok {
				return nil
			}
			if previous, known := seen[stat.Ino]; known {
				previous.roots[root] = true
				// The same bytes under a second name. The later path alphabetically wins, which is
				// films/ or series/ over downloads/: the same number either way, and the library
				// path is the one worth reading on a panel.
				if path > previous.path {
					previous.path = path
				}
				seen[stat.Ino] = previous
				return nil
			}
			seen[stat.Ino] = biggest{info.Size(), path, map[string]bool{root: true}}
			perRoot[root] += info.Size()
			return nil
		})
	}

	lines := []string{
		"# HELP disk_file_bytes Size in bytes of the largest individual files on the data disk.",
		"# TYPE disk_file_bytes gauge",
	}
	ranked := make([]biggest, 0, len(seen))
	for _, item := range seen {
		ranked = append(ranked, item)
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].size > ranked[j].size })
	if len(ranked) > topFiles {
		ranked = ranked[:topFiles]
	}
	for _, item := range ranked {
		lines = append(lines, fmt.Sprintf("disk_file_bytes{root=%q,path=%q} %d",
			escape(topOf(item.path)), escape(item.path), item.size))
	}

	lines = append(lines,
		"# HELP disk_root_bytes Size in bytes of each top-level folder on the data disk.",
		"# TYPE disk_root_bytes gauge")
	roots := make([]string, 0, len(perRoot))
	for name := range perRoot {
		roots = append(roots, name)
	}
	sort.Slice(roots, func(i, j int) bool { return perRoot[roots[i]] > perRoot[roots[j]] })
	for _, name := range roots {
		lines = append(lines, fmt.Sprintf("disk_root_bytes{root=%q} %d", escape(name), perRoot[name]))
	}

	lines = append(lines, purpose(seen)...)

	lines = append(lines,
		"# HELP disk_usage_scrape_timestamp_seconds Unix time this walk finished.",
		"# TYPE disk_usage_scrape_timestamp_seconds gauge",
		fmt.Sprintf("disk_usage_scrape_timestamp_seconds %d", time.Now().Unix()))
	return lines, problems
}

func topOf(path string) string {
	relative := strings.TrimPrefix(path, dataRoot+string(filepath.Separator))
	return strings.SplitN(relative, string(filepath.Separator), 2)[0]
}

func contains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}
