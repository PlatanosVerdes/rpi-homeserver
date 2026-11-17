// Package originals lists the .original files audio-compat.py and add-audio.py leave beside a file they
// rewrite, and deletes one when asked. Nothing here deletes on its own: pruning is the user's decision.
package originals

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"audio-manager/internal/httpx"
	"audio-manager/internal/libraries"
)

// listFor is how long one walk of the library answers the page's polls.
const listFor = time.Minute

// The names audio-compat's free_original_name gives: .original, then .original.2 and up.
var originalName = regexp.MustCompile(`^(.+)\.original(\.[2-9]|\.[1-9][0-9]+)?$`)

// File is one .original, its path from the data root down, like a library_path.
type File struct {
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	Time  int64  `json:"time"`
	Links uint64 `json:"links"`
	Live  bool   `json:"live"`
}

type Store struct {
	dataRoot string

	mu     sync.Mutex
	listed time.Time
	files  []File
}

func New(dataRoot string) *Store { return &Store{dataRoot: dataRoot} }

// liveName is the file an .original was kept for, or "" when the name is not an .original's.
func liveName(name string) string {
	m := originalName.FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	return m[1]
}

// List is every .original under the library folders, walked again at most once a listFor.
func (s *Store) List() []File {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.files == nil || time.Since(s.listed) >= listFor {
		s.files, s.listed = s.walk(), time.Now()
	}
	return s.files
}

func (s *Store) walk() []File {
	files := []File{}
	for _, folder := range libraries.Folders {
		filepath.WalkDir(filepath.Join(s.dataRoot, folder), func(full string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return nil
			}
			live := liveName(d.Name())
			if live == "" {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			rel, err := filepath.Rel(s.dataRoot, full)
			if err != nil {
				return nil
			}
			files = append(files, File{Path: filepath.ToSlash(rel), Size: info.Size(), Time: info.ModTime().Unix(),
				Links: libraries.Links(info), Live: isRegular(filepath.Join(filepath.Dir(full), live))})
			return nil
		})
	}
	return files
}

func isRegular(file string) bool {
	info, err := os.Stat(file)
	return err == nil && info.Mode().IsRegular()
}

var errHardlinked = errors.New("still seeding (hardlinked): deleting it frees nothing until the torrent is removed; confirm to delete it anyway")

// resolve turns a listed path into the file to delete, refusing anything but an .original inside a
// library folder, with its live file beside it.
func (s *Store) resolve(rel string) (string, fs.FileInfo, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", nil, errors.New("not a path inside the library")
	}
	name := filepath.Base(rel)
	live := liveName(name)
	if live == "" {
		return "", nil, errors.New("not an .original file")
	}
	// The directory is resolved and the file is not, so a symlink named .original is refused, never followed.
	dir, err := filepath.EvalSymlinks(filepath.Dir(filepath.Join(s.dataRoot, filepath.FromSlash(rel))))
	if err != nil {
		return "", nil, errors.New("no such file")
	}
	if !s.insideFolder(dir) {
		return "", nil, errors.New("not a path inside the library")
	}
	full := filepath.Join(dir, name)
	info, err := os.Lstat(full)
	if err != nil {
		return "", nil, errors.New("no such file")
	}
	if !info.Mode().IsRegular() {
		return "", nil, errors.New("not a regular file")
	}
	if !isRegular(filepath.Join(dir, live)) {
		return "", nil, fmt.Errorf("%s is not beside it, so this may be the only copy", live)
	}
	return full, info, nil
}

func (s *Store) insideFolder(dir string) bool {
	for _, folder := range libraries.Folders {
		root, err := filepath.EvalSymlinks(filepath.Join(s.dataRoot, folder))
		if err != nil {
			continue
		}
		if rel, err := filepath.Rel(root, dir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// Delete removes one listed .original. A hardlinked one is still seeding and needs allowHardlinked.
func (s *Store) Delete(rel string, allowHardlinked bool) (int, error) {
	full, info, err := s.resolve(rel)
	if err != nil {
		return http.StatusBadRequest, err
	}
	if libraries.Links(info) > 1 && !allowHardlinked {
		return http.StatusConflict, errHardlinked
	}
	if err := os.Remove(full); err != nil {
		return http.StatusInternalServerError, fmt.Errorf("deleting %s: %w", rel, err)
	}
	s.mu.Lock()
	s.files = nil
	s.mu.Unlock()
	return http.StatusOK, nil
}

func (s *Store) ServeDelete(w http.ResponseWriter, r *http.Request) {
	if !httpx.PostOnly(w, r) {
		return
	}
	code, err := s.Delete(strings.TrimSpace(r.FormValue("path")), r.FormValue("allow_hardlinked") != "")
	if err != nil {
		body := map[string]any{"error": err.Error()}
		if code == http.StatusConflict {
			body["confirm"] = true
		}
		httpx.WriteJSON(w, code, body)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
