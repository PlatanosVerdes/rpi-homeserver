package main

import (
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"
)

// The library scan lists the files the nightly pass would convert before any client has played
// them. audio-compat.py --scan decides, so the page shows exactly what the night run would do.

type libraryScan struct {
	Time  int64        `json:"time"`
	Files []ledgerFile `json:"files"`
	// The codecs --target takes, as the script defines them, so the page offers no other.
	Targets       map[string]target `json:"targets"`
	DefaultTarget string            `json:"default_target"`
	Error         string            `json:"error,omitempty"`
}

type target struct {
	Codec       string `json:"codec"`
	BitrateKbps int    `json:"bitrate_kbps"`
	MaxChannels int    `json:"max_channels"`
}

func (s *server) scanFile() string { return filepath.Join(s.stateDir, "library.json") }

func (s *server) loadScan() {
	raw, err := os.ReadFile(s.scanFile())
	if err != nil {
		return
	}
	var last libraryScan
	if json.Unmarshal(raw, &last) == nil {
		s.mu.Lock()
		s.library = last
		s.mu.Unlock()
	}
}

// scanner rescans on start, every scanEvery, and after each conversion (rescan).
func (s *server) scanner() {
	timer := time.NewTimer(0)
	for {
		select {
		case <-timer.C:
		case <-s.rescan:
			timer.Stop()
		}
		s.scan()
		timer.Reset(s.scanEvery)
	}
}

func (s *server) scan() {
	out, err := exec.Command("nice", "-n", "10", s.python, s.script, "--scan").Output()
	result := libraryScan{Time: time.Now().Unix()}
	if err != nil {
		result.Error = "library scan failed: " + err.Error()
		log.Printf("audio-convert: %s", result.Error)
	} else if err := json.Unmarshal(out, &result); err != nil {
		result = libraryScan{Time: time.Now().Unix(), Error: "library scan returned bad JSON: " + err.Error()}
		log.Printf("audio-convert: %s", result.Error)
	}

	s.mu.Lock()
	if result.Error != "" {
		// Keep showing the last good list; only the error is new.
		result.Files, result.Targets, result.DefaultTarget = s.library.Files, s.library.Targets, s.library.DefaultTarget
	}
	s.library = result
	s.mu.Unlock()

	if raw, err := json.Marshal(result); err == nil {
		tmp := s.scanFile() + ".tmp"
		if os.WriteFile(tmp, raw, 0o644) == nil {
			os.Rename(tmp, s.scanFile())
		}
	}
}

// validTarget says whether the script takes this --target, going by its last scan.
func (s *server) validTarget(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.library.Targets[name]
	return ok
}

func (s *server) requestRescan() {
	select {
	case s.rescan <- struct{}{}:
	default:
	}
}

// libraryFiles is the last scan minus what the ledger already lists, so a file is shown once.
func (s *server) libraryFiles(ledger []ledgerFile) (libraryScan, []ledgerFile) {
	s.mu.Lock()
	last := s.library
	s.mu.Unlock()
	files := slices.DeleteFunc(slices.Clone(last.Files), func(f ledgerFile) bool {
		return slices.ContainsFunc(ledger, func(l ledgerFile) bool { return l.Library == f.Library })
	})
	// Seen is when the scan last found the file needing work, so a conversion older than that scan
	// (the file was replaced or restored since) does not mark it done.
	for i := range files {
		files[i].Seen = last.Time
	}
	return last, files
}
