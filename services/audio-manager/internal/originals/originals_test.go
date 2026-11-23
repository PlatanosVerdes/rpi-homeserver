package originals

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// library builds a data root holding the given files, and returns it.
func library(t *testing.T, files ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range files {
		write(t, filepath.Join(root, filepath.FromSlash(f)))
	}
	return root
}

func TestLiveName(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, want string }{
		{"A.mkv.original", "A.mkv"},
		{"A.mkv.original.2", "A.mkv"},
		{"A.mkv.original.12", "A.mkv"},
		{"A.mkv.original.1", ""},
		{"A.mkv.original.02", ""},
		{"A.mkv.original.x", ""},
		{"A.mkv", ""},
		{".original", ""},
		{"A.original.mkv", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := liveName(c.name); got != c.want {
				t.Errorf("liveName(%q) = %q, want %q", c.name, got, c.want)
			}
		})
	}
}

func TestList(t *testing.T) {
	t.Parallel()
	root := library(t,
		"films/A (2020)/A.mkv", "films/A (2020)/A.mkv.original", "films/A (2020)/A.mkv.original.2",
		"films/B (2021)/B.mkv.original",
		"series/S/Season 1/S01E01.mkv", "series/S/Season 1/S01E01.mkv.original",
		"films/C (2022)/C.mkv",
		"downloads/D.mkv", "downloads/D.mkv.original",
	)
	if err := os.Symlink(filepath.Join(root, "films/C (2022)/C.mkv"), filepath.Join(root, "films/C (2022)/C.mkv.original")); err != nil {
		t.Fatal(err)
	}
	got := New(root).List()
	want := []File{
		{Path: "films/A (2020)/A.mkv.original", Live: true},
		{Path: "films/A (2020)/A.mkv.original.2", Live: true},
		{Path: "films/B (2021)/B.mkv.original", Live: false},
		{Path: "series/S/Season 1/S01E01.mkv.original", Live: true},
	}
	if len(got) != len(want) {
		t.Fatalf("listed %+v, want %d files", got, len(want))
	}
	for i, f := range got {
		if f.Path != want[i].Path || f.Live != want[i].Live || f.Size != int64(len(filepath.Join(root, f.Path))) || f.Time == 0 || f.Links != 1 {
			t.Errorf("file %d: %+v, want %+v with its size, time and one link", i, f, want[i])
		}
	}
}

func TestListIsCachedUntilADelete(t *testing.T) {
	t.Parallel()
	root := library(t, "films/A/A.mkv", "films/A/A.mkv.original")
	s := New(root)
	if n := len(s.List()); n != 1 {
		t.Fatalf("listed %d", n)
	}
	if err := os.WriteFile(filepath.Join(root, "films/A/A.mkv.original.2"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if n := len(s.List()); n != 1 {
		t.Fatalf("a second list within a minute walked again: %d", n)
	}
	if code, err := s.Delete("films/A/A.mkv.original", false); err != nil {
		t.Fatalf("%d %v", code, err)
	}
	if got := s.List(); len(got) != 1 || got[0].Path != "films/A/A.mkv.original.2" {
		t.Fatalf("after a delete: %+v", got)
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		path    string
		allow   bool
		code    int
		error   string
		deletes string
		prepare func(t *testing.T, root, outside string)
	}{
		{name: "an original with its live file", path: "films/A/A.mkv.original", code: 200, deletes: "films/A/A.mkv.original"},
		{name: "a numbered original", path: "films/A/A.mkv.original.2", code: 200, deletes: "films/A/A.mkv.original.2"},
		{name: "an episode's original", path: "series/S/S01E01.mkv.original", code: 200, deletes: "series/S/S01E01.mkv.original"},
		{name: "a path that is not cleaned", path: "films/A/../A/./A.mkv.original", code: 200, deletes: "films/A/A.mkv.original"},
		{name: "empty", path: "", code: 400, error: "not a path inside"},
		{name: "absolute", path: "/etc/passwd.original", code: 400, error: "not a path inside"},
		{name: "the live file itself", path: "films/A/A.mkv", code: 400, error: "not an .original"},
		{name: "a name that only looks like one", path: "films/A/A.mkv.original.x", code: 400, error: "not an .original"},
		{name: "live file missing", path: "films/B/B.mkv.original", code: 400, error: "may be the only copy"},
		{name: "no such file", path: "films/A/Z.mkv.original", code: 400, error: "no such file"},
		{name: "no such folder", path: "films/Z/Z.mkv.original", code: 400, error: "no such file"},
		{name: "climbing out of the data root", path: "../outside/O.mkv.original", code: 400, error: "not a path inside"},
		{name: "climbing out through a library folder", path: "films/../../outside/O.mkv.original", code: 400, error: "not a path inside"},
		{name: "outside the library folders", path: "downloads/D.mkv.original", code: 400, error: "not a path inside"},
		{
			name: "a folder symlinked out of the library", path: "films/Escape/O.mkv.original", code: 400, error: "not a path inside",
			prepare: func(t *testing.T, root, outside string) {
				if err := os.Symlink(outside, filepath.Join(root, "films/Escape")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "an .original that is a symlink to the live file", path: "films/C/C.mkv.original", code: 400, error: "not a regular file",
			prepare: func(t *testing.T, root, outside string) {
				if err := os.Symlink(filepath.Join(root, "films/C/C.mkv"), filepath.Join(root, "films/C/C.mkv.original")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "a directory named .original", path: "films/C/D.mkv.original", code: 400, error: "not a regular file",
			prepare: func(t *testing.T, root, outside string) {
				if err := os.Mkdir(filepath.Join(root, "films/C/D.mkv.original"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "films/C/D.mkv"), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "still seeding", path: "films/A/A.mkv.original", code: 409, error: "still seeding",
			prepare: func(t *testing.T, root, outside string) {
				if err := os.Link(filepath.Join(root, "films/A/A.mkv.original"), filepath.Join(root, "downloads/A.mkv")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "still seeding, confirmed", path: "films/A/A.mkv.original", allow: true, code: 200, deletes: "films/A/A.mkv.original",
			prepare: func(t *testing.T, root, outside string) {
				if err := os.Link(filepath.Join(root, "films/A/A.mkv.original"), filepath.Join(root, "downloads/A.mkv")); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			base := t.TempDir()
			root := filepath.Join(base, "data")
			outside := filepath.Join(base, "outside")
			for _, f := range []string{
				"data/films/A/A.mkv", "data/films/A/A.mkv.original", "data/films/A/A.mkv.original.2", "data/films/B/B.mkv.original",
				"data/films/C/C.mkv", "data/series/S/S01E01.mkv", "data/series/S/S01E01.mkv.original",
				"data/downloads/D.mkv", "data/downloads/D.mkv.original", "outside/O.mkv", "outside/O.mkv.original",
			} {
				write(t, filepath.Join(base, f))
			}
			write(t, filepath.Join(outside, "O.mkv.original"))
			if c.prepare != nil {
				c.prepare(t, root, outside)
			}
			before := snapshot(t, base)

			form := url.Values{"path": {c.path}}
			if c.allow {
				form.Set("allow_hardlinked", "1")
			}
			req := httptest.NewRequest(http.MethodPost, "/api/originals/delete", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			New(root).ServeDelete(rec, req)

			if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.error) {
				t.Fatalf("got %d %s, want %d naming %q", rec.Code, rec.Body.String(), c.code, c.error)
			}
			after := snapshot(t, base)
			if c.deletes != "" {
				before = slices.DeleteFunc(before, func(p string) bool { return p == filepath.Join("data", filepath.FromSlash(c.deletes)) })
			}
			if !slices.Equal(before, after) {
				t.Errorf("files after: %v, want %v", after, before)
			}
		})
	}
}

func TestDeleteIsPostOnly(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	New(t.TempDir()).ServeDelete(rec, httptest.NewRequest(http.MethodGet, "/api/originals/delete?path=films/A/A.mkv.original", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET answered %d", rec.Code)
	}
}

func write(t *testing.T, file string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
}

// snapshot lists every path under dir, symlinks included, so a test sees anything deleted.
func snapshot(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(full string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, full)
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
