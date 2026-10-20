package main

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func speed(v float64) *float64 { return &v }

func TestETA(t *testing.T) {
	cases := []struct{ duration, done, speed, want float64 }{
		{7200, 0, 20, 360},
		{7200, 3600, 20, 180},
		{7200, 8000, 20, 0},
		{600, 0, 0, 30}, // no measured speed yet falls back to 20x
	}
	for _, c := range cases {
		if got := eta(c.duration, c.done, c.speed); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("eta(%v, %v, %v) = %v, want %v", c.duration, c.done, c.speed, got, c.want)
		}
	}
}

func TestAverageSpeedUsesTheLastSuccessfulRuns(t *testing.T) {
	if got := averageSpeed(nil); got != measuredSpeed {
		t.Fatalf("empty history: %v", got)
	}
	history := []historyEntry{
		{Result: "done", Speed: speed(100)}, // outside the window once five newer ones exist
		{Result: "done", Speed: speed(10)},
		{Result: "done", Speed: speed(20)},
		{Result: "failed", Speed: speed(500)},
		{Result: "done", Speed: nil},
		{Result: "done", Speed: speed(30)},
		{Result: "done", Speed: speed(20)},
		{Result: "done", Speed: speed(20)},
	}
	if got := averageSpeed(history); got != 20 {
		t.Fatalf("average %v, want 20", got)
	}
}

func TestParseEvent(t *testing.T) {
	lines := []string{
		`{"event": "plan", "path": "/mnt/data/films/a.mkv", "duration": 600.0, "size": 1, "links": 1, "source": {"position": 1, "codec": "dts"}}`,
		`{"event": "progress", "seconds": 122.0, "duration": 600.0, "speed": 24.2}`,
		`{"event": "progress", "seconds": 0, "duration": 600.0, "speed": null}`,
		`{"event": "failed", "reason": "duration moved: 600.000 -> 282.080"}`,
		`not json`,
		`{"no": "event"}`,
	}
	plan, ok := parseEvent([]byte(lines[0]))
	if !ok || plan.Event != "plan" || plan.Duration != 600 || plan.Source["codec"] != "dts" {
		t.Fatalf("plan: %+v", plan)
	}
	p, ok := parseEvent([]byte(lines[1]))
	if !ok || p.Seconds != 122 || p.Speed == nil || *p.Speed != 24.2 {
		t.Fatalf("progress: %+v", p)
	}
	if p, _ := parseEvent([]byte(lines[2])); p.Speed != nil {
		t.Fatalf("null speed parsed as %v", *p.Speed)
	}
	if f, _ := parseEvent([]byte(lines[3])); f.Reason != "duration moved: 600.000 -> 282.080" {
		t.Fatalf("failed: %+v", f)
	}
	for _, line := range lines[4:] {
		if _, ok := parseEvent([]byte(line)); ok {
			t.Fatalf("parsed %q", line)
		}
	}
}

func TestLockHolder(t *testing.T) {
	file := filepath.Join(t.TempDir(), "convert.lock")
	if holder, err := lockHolder(file); err != nil || holder != nil {
		t.Fatalf("free lock: %v %v", holder, err)
	}

	handle, err := os.OpenFile(file, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(handle.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	handle.WriteString(`{"mode": "nightly", "file": "/mnt/data/films/X/x.mkv"}`)
	holder, err := lockHolder(file)
	if err != nil || holder == nil || holder["mode"] != "nightly" {
		t.Fatalf("held lock: %v %v", holder, err)
	}

	handle.Close()
	if holder, _ := lockHolder(file); holder != nil {
		t.Fatalf("released lock still reads as held: %v", holder)
	}
}

func TestInsideLibrary(t *testing.T) {
	for library, want := range map[string]bool{
		"films/A (2020)/a.mkv":     true,
		"series/S/s01e01.mkv":      true,
		"films/../downloads/x.mkv": false,
		"downloads/x.mkv":          false,
		"films/../../etc/passwd":   false,
		"":                         false,
	} {
		if _, ok := insideLibrary("/mnt/data", library); ok != want {
			t.Errorf("%q: %v, want %v", library, ok, want)
		}
	}
}

// fixture is a server over a temporary library and a fake audio-compat that prints what it is given.
func fixture(t *testing.T, script string) (*server, string) {
	t.Helper()
	dir := t.TempDir()
	s := newServer()
	s.stateDir = filepath.Join(dir, "state")
	s.stateFile = defaultStateFile(s.stateDir)
	s.lockFile = filepath.Join(s.stateDir, "convert.lock")
	s.history = filepath.Join(s.stateDir, "history.jsonl")
	s.ledgerFile = filepath.Join(dir, "audio-recodes.json")
	s.dataRoot = filepath.Join(dir, "data")
	s.python = "sh"
	s.script = filepath.Join(dir, "fake.sh")
	s.retry = 10 * time.Millisecond
	os.MkdirAll(s.stateDir, 0o755)
	os.MkdirAll(filepath.Join(s.dataRoot, "films", "A"), 0o755)
	os.WriteFile(filepath.Join(s.dataRoot, "films", "A", "a.mkv"), []byte("x"), 0o644)
	os.WriteFile(s.script, []byte(script), 0o755)
	ledger := `{"files":[
		{"path":"/data/films/A/a.mkv","codec":"dca","app":"plex","client":"TV","seen":100,"cause":"codec",
		 "title":"A (2020)","duration":600,"library_path":"films/A/a.mkv","actionable":true,
		 "plan":{"source":{"position":0,"codec":"dts"},"target_codec":"ac3","target_bitrate_kbps":640,"target_channels":6,"extra_bytes":48000000}},
		{"path":"/data/films/B/b.mkv","codec":"eac3","app":"plex","client":"TV","seen":200,"cause":"no_direct_play",
		 "library_path":"films/B/b.mkv","actionable":false,"reason_code":"direct_play_off"}]}`
	os.WriteFile(s.ledgerFile, []byte(ledger), 0o644)
	return s, filepath.Join(s.dataRoot, "films", "A", "a.mkv")
}

func TestRunReadsProgressAndTheResult(t *testing.T) {
	s, _ := fixture(t, `
case "$*" in *--allow-hardlinked*) echo '{"event":"failed","reason":"wrong flag"}'; exit 1;; esac
case "$*" in *"--track 3"*) echo '{"event":"failed","reason":"track 3"}'; exit 1;; esac
echo '{"event":"plan","duration":600,"source":{"position":0}}'
echo '{"event":"progress","seconds":300,"duration":600,"speed":25}'
echo "file=$2" >&2
echo '{"event":"done","seconds":24}'
`)
	result, busy := s.run(job{Library: "films/A/a.mkv"})
	if busy || result.Result != "done" {
		t.Fatalf("run: %+v busy=%v", result, busy)
	}

	result, _ = s.run(job{Library: "films/A/a.mkv", AllowHardlinked: true})
	if result.Result != "failed" || result.Reason != "wrong flag" {
		t.Fatalf("flag not passed through: %+v", result)
	}

	three := 3
	if result, _ = s.run(job{Library: "films/A/a.mkv", Track: &three}); result.Reason != "track 3" {
		t.Fatalf("track not passed through: %+v", result)
	}
}

func TestRunKeepsABusyFileQueued(t *testing.T) {
	s, _ := fixture(t, `echo '{"event":"failed","reason":"busy"}'; exit 75`)
	if _, busy := s.run(job{Library: "films/A/a.mkv"}); !busy {
		t.Fatal("exit 75 not read as busy")
	}
	s2, _ := fixture(t, `echo 'Traceback: boom' >&2; exit 1`)
	if result, busy := s2.run(job{Library: "films/A/a.mkv"}); busy || result.Reason != "Traceback: boom" {
		t.Fatalf("crash: %+v", result)
	}
}

func TestWorkerConvertsAndWaitsForTheNightRun(t *testing.T) {
	s, _ := fixture(t, `echo '{"event":"done"}'`)

	held, _ := os.OpenFile(s.lockFile, os.O_RDWR|os.O_CREATE, 0o644)
	syscall.Flock(int(held.Fd()), syscall.LOCK_EX)
	held.WriteString(`{"mode":"nightly","file":"/mnt/data/films/X/x.mkv"}`)

	go s.worker()
	if !s.enqueue(job{Library: "films/A/a.mkv"}) || s.enqueue(job{Library: "films/A/a.mkv"}) {
		t.Fatal("enqueue should take a file once")
	}
	time.Sleep(100 * time.Millisecond)
	s.mu.Lock()
	waiting, queued := s.waiting, len(s.queue)
	s.mu.Unlock()
	if waiting == nil || waiting["mode"] != "nightly" || queued != 1 {
		t.Fatalf("should wait for the night run: waiting=%v queued=%d", waiting, queued)
	}

	held.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		done := s.results["films/A/a.mkv"].Result
		s.mu.Unlock()
		if done == "done" {
			saved, _ := os.ReadFile(s.stateFile)
			if !strings.Contains(string(saved), `"result": "done"`) {
				t.Fatalf("result not persisted: %s", saved)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("never converted once the lock was free")
}

func TestPlayChecks(t *testing.T) {
	s, file := fixture(t, `echo '{"event":"done"}'`)
	post := func(values url.Values) (int, map[string]any) {
		r := httptest.NewRequest("POST", "/api/play", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		s.play(w, r)
		var body map[string]any
		json.NewDecoder(w.Body).Decode(&body)
		return w.Code, body
	}
	// The free-space rule asks for 40 GiB on top of the file, which a test disk may not have.
	s.dataRoot, _ = filepath.Abs(s.dataRoot)

	if code, _ := post(url.Values{"library_path": {"films/B/b.mkv"}}); code != http.StatusBadRequest {
		t.Errorf("non-actionable file accepted: %d", code)
	}
	if code, _ := post(url.Values{"library_path": {"films/../../x"}}); code != http.StatusBadRequest {
		t.Errorf("unlisted path accepted: %d", code)
	}

	os.Link(file, filepath.Join(filepath.Dir(file), "seed.mkv"))
	if code, body := post(url.Values{"library_path": {"films/A/a.mkv"}}); code != http.StatusConflict || body["confirm"] != true {
		t.Errorf("hardlinked file queued without confirmation: %d %v", code, body)
	}
	code, body := post(url.Values{"library_path": {"films/A/a.mkv"}, "allow_hardlinked": {"1"}})
	if freeBytes(s.dataRoot) < 1+minFreeBytes {
		if code != http.StatusConflict {
			t.Errorf("queued with too little free space: %d %v", code, body)
		}
		return
	}
	if code != http.StatusOK || len(s.queue) != 1 || !s.queue[0].AllowHardlinked || s.queue[0].Track == nil || *s.queue[0].Track != 0 {
		t.Errorf("confirmed file not queued: %d %v %+v", code, body, s.queue)
	}
}

func TestItemsStatus(t *testing.T) {
	s, _ := fixture(t, "")
	history := []historyEntry{{Time: 50, Mode: "nightly", Path: "/mnt/data/films/A/a.mkv", Result: "done"}}
	files, _ := readLedger(s.ledgerFile)

	items := s.items(files, history, 20)
	if items[0].Library != "films/A/a.mkv" || items[0].Status != "ready" || items[0].ETA != 30 {
		t.Fatalf("a conversion older than the session should not count: %+v", items[0])
	}
	if items[1].Status != "info" || items[1].Actionable {
		t.Fatalf("direct play off: %+v", items[1])
	}

	history[0].Time = 150
	if items = s.items(files, history, 20); items[0].Status != "done" || items[0].Outcome.Mode != "nightly" {
		t.Fatalf("night run's conversion not shown: %+v", items[0])
	}

	s.queue = []job{{Library: "films/A/a.mkv"}}
	if items = s.items(files, nil, 20); items[0].Status != "queued" {
		t.Fatalf("queued: %+v", items[0])
	}
	s.queue, s.running = nil, &progress{job: job{Library: "films/A/a.mkv"}}
	if items = s.items(files, nil, 20); items[0].Status != "converting" {
		t.Fatalf("running: %+v", items[0])
	}
}
