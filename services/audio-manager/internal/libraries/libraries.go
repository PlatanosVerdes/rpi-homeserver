// Package libraries names the folders under the data root that audio-compat.py and add-audio.py rewrite,
// and what both views ask of a file in them.
package libraries

import (
	"io/fs"
	"syscall"
)

var Folders = []string{"films", "series"}

// Links is a file's hardlink count: more than one means a torrent still seeds it.
func Links(info fs.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink)
	}
	return 1
}
