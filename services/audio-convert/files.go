package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// What pi-metrics writes (services/pi-metrics/recodes.go and plan.go). Only the fields shown here.
type ledgerFile struct {
	Path         string  `json:"path"`
	Codec        string  `json:"codec"`
	App          string  `json:"app"`
	Client       string  `json:"client"`
	Seen         int64   `json:"seen"`
	Cause        string  `json:"cause"`
	Title        string  `json:"title"`
	Duration     float64 `json:"duration"`
	Library      string  `json:"library_path"`
	Actionable   bool    `json:"actionable"`
	Reason       string  `json:"reason"`
	ReasonCode   string  `json:"reason_code"`
	ReasonDetail string  `json:"reason_detail"`
	Plan         *struct {
		Source        planTrack   `json:"source"`
		Sources       []planTrack `json:"sources"`
		TargetCodec   string      `json:"target_codec"`
		TargetBitrate int         `json:"target_bitrate_kbps"`
		TargetChannel int         `json:"target_channels"`
		ExtraBytes    int64       `json:"extra_bytes"`
		Summary       string      `json:"summary"`
	} `json:"plan"`
}

type planTrack struct {
	Position int    `json:"position"`
	Codec    string `json:"codec"`
	Language string `json:"language"`
	Channels int    `json:"channels"`
	Default  bool   `json:"default"`
	Played   bool   `json:"played"`
	Title    string `json:"title"`
}

func readLedger(file string) ([]ledgerFile, error) {
	raw, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc struct {
		Files []ledgerFile `json:"files"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	for i := range doc.Files {
		if doc.Files[i].Library == "" {
			doc.Files[i].Library = libraryPath(doc.Files[i].Path)
		}
	}
	return doc.Files, nil
}

var libraries = []string{"films", "series"}

// libraryPath is a path from its library folder down, the rule audio-compat's library_path uses.
func libraryPath(reported string) string {
	parts := strings.Split(path.Clean(reported), "/")
	for position, part := range parts {
		if slices.Contains(libraries, part) {
			return strings.Join(parts[position:], "/")
		}
	}
	return ""
}

// One conversion audio-compat attempted, from the page or the night run.
type historyEntry struct {
	Time     int64    `json:"time"`
	Mode     string   `json:"mode"`
	Path     string   `json:"path"`
	Name     string   `json:"name"`
	Result   string   `json:"result"`
	Reason   string   `json:"reason,omitempty"`
	Duration float64  `json:"duration"`
	Seconds  float64  `json:"seconds"`
	Speed    *float64 `json:"speed"`
}

func readHistory(file string) ([]historyEntry, error) {
	handle, err := os.Open(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	var out []historyEntry
	scanner := bufio.NewScanner(handle)
	for scanner.Scan() {
		var entry historyEntry
		if json.Unmarshal(scanner.Bytes(), &entry) == nil {
			out = append(out, entry)
		}
	}
	return out, scanner.Err()
}

const (
	measuredSpeed = 20.0 // realtime factor of an AC3 remux on this Pi, before any run is recorded
	speedWindow   = 5
)

// averageSpeed is the mean realtime factor of the last few successful conversions.
func averageSpeed(history []historyEntry) float64 {
	var speeds []float64
	for i := len(history) - 1; i >= 0 && len(speeds) < speedWindow; i-- {
		if entry := history[i]; entry.Result == "done" && entry.Speed != nil && *entry.Speed > 0 {
			speeds = append(speeds, *entry.Speed)
		}
	}
	if len(speeds) == 0 {
		return measuredSpeed
	}
	sum := 0.0
	for _, speed := range speeds {
		sum += speed
	}
	return sum / float64(len(speeds))
}

// eta is how long converting the rest of a film takes at a realtime factor, in seconds.
func eta(duration, done, speed float64) float64 {
	if speed <= 0 {
		speed = measuredSpeed
	}
	left := duration - done
	if left < 0 {
		left = 0
	}
	return left / speed
}

// lockHolder says who holds audio-compat's flock, or nil when nobody does. Taking it and letting go
// at once is the only way to ask: the file's contents outlive a holder killed with -9.
func lockHolder(file string) (map[string]any, error) {
	handle, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	if err := syscall.Flock(int(handle.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		syscall.Flock(int(handle.Fd()), syscall.LOCK_UN)
		return nil, nil
	} else if !errors.Is(err, syscall.EWOULDBLOCK) {
		return nil, err
	}
	holder := map[string]any{}
	raw, _ := os.ReadFile(file)
	json.Unmarshal(raw, &holder)
	return holder, nil
}

type fileFacts struct {
	exists bool
	size   int64
	links  uint64
	free   uint64
}

func inspect(file string) fileFacts {
	info, err := os.Stat(file)
	if err != nil {
		return fileFacts{}
	}
	facts := fileFacts{exists: true, size: info.Size(), links: 1}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		facts.links = uint64(st.Nlink)
	}
	facts.free = freeBytes(filepath.Dir(file))
	return facts
}

func freeBytes(dir string) uint64 {
	var st syscall.Statfs_t
	if syscall.Statfs(dir, &st) != nil {
		return 0
	}
	return uint64(st.Bavail) * uint64(st.Bsize)
}

// insideLibrary joins a ledger's library path onto the data root, refusing anything that climbs out.
func insideLibrary(root, library string) (string, bool) {
	clean := path.Clean("/" + library)[1:]
	if library == "" || clean != library || !slices.Contains(libraries, strings.SplitN(clean, "/", 2)[0]) {
		return "", false
	}
	return filepath.Join(root, filepath.FromSlash(clean)), true
}
