package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// The conversion itself is scripts/sync/audio-compat.py --file, the same remux, verify and rename
// the night run does; this only queues files for it and reads what it prints.

const exitBusy = 75 // audio-compat's EX_TEMPFAIL: the other side holds the lock

type job struct {
	Library         string `json:"library_path"`
	Title           string `json:"title"`
	AllowHardlinked bool   `json:"allow_hardlinked"`
	Added           int64  `json:"added"`
}

type outcome struct {
	Time   int64  `json:"time"`
	Result string `json:"result"` // done or failed
	Reason string `json:"reason,omitempty"`
	Mode   string `json:"mode,omitempty"` // who converted it, when the night run did
}

type progress struct {
	job
	Started  int64          `json:"started"`
	Seconds  float64        `json:"seconds"`
	Duration float64        `json:"duration"`
	Speed    float64        `json:"speed"`
	Source   map[string]any `json:"source,omitempty"`
}

// event is one line of audio-compat --progress.
type event struct {
	Event    string         `json:"event"`
	Seconds  float64        `json:"seconds"`
	Duration float64        `json:"duration"`
	Speed    *float64       `json:"speed"`
	Reason   string         `json:"reason"`
	Source   map[string]any `json:"source"`
}

func parseEvent(line []byte) (event, bool) {
	var e event
	if json.Unmarshal(bytes.TrimSpace(line), &e) != nil || e.Event == "" {
		return event{}, false
	}
	return e, true
}

type persisted struct {
	Queue   []job              `json:"queue"`
	Results map[string]outcome `json:"results"`
}

func (s *server) load() {
	raw, err := os.ReadFile(s.stateFile)
	if err != nil {
		return
	}
	var saved persisted
	if err := json.Unmarshal(raw, &saved); err != nil {
		log.Printf("audio-convert: %s: %v", s.stateFile, err)
		return
	}
	s.queue = saved.Queue
	if saved.Results != nil {
		s.results = saved.Results
	}
}

// save is called with s.mu held.
func (s *server) save() {
	raw, err := json.MarshalIndent(persisted{Queue: s.queue, Results: s.results}, "", "  ")
	if err != nil {
		return
	}
	tmp := s.stateFile + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		log.Printf("audio-convert: %v", err)
		return
	}
	if err := os.Rename(tmp, s.stateFile); err != nil {
		log.Printf("audio-convert: %v", err)
	}
}

func (s *server) enqueue(j job) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if (s.running != nil && s.running.Library == j.Library) ||
		slices.ContainsFunc(s.queue, func(q job) bool { return q.Library == j.Library }) {
		return false
	}
	s.queue = append(s.queue, j)
	s.save()
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return true
}

func (s *server) dequeue(library string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := len(s.queue)
	s.queue = slices.DeleteFunc(s.queue, func(q job) bool { return q.Library == library })
	if len(s.queue) == before {
		return false
	}
	s.save()
	return true
}

// worker runs the queue one file at a time, forever. A file the night run is busy with, or any file
// while it runs, stays at the head of the queue until the lock is free.
func (s *server) worker() {
	for {
		s.mu.Lock()
		var next *job
		if len(s.queue) > 0 {
			head := s.queue[0]
			next = &head
		}
		s.mu.Unlock()
		if next == nil {
			select {
			case <-s.wake:
			case <-time.After(time.Minute):
			}
			continue
		}

		if holder, _ := lockHolder(s.lockFile); holder != nil {
			s.setWaiting(holder)
			select {
			case <-s.wake:
			case <-time.After(s.retry):
			}
			continue
		}
		s.setWaiting(nil)

		result, busy := s.run(*next)
		s.mu.Lock()
		s.running = nil
		if !busy {
			s.queue = slices.DeleteFunc(s.queue, func(q job) bool { return q.Library == next.Library })
			s.results[next.Library] = result
			log.Printf("audio-convert: %s: %s %s", next.Library, result.Result, result.Reason)
		}
		s.save()
		s.mu.Unlock()
		if busy {
			time.Sleep(s.retry)
		}
	}
}

func (s *server) setWaiting(holder map[string]any) {
	s.mu.Lock()
	s.waiting = holder
	s.mu.Unlock()
}

// run converts one file and says how it went, or busy when the night run took the lock first.
func (s *server) run(j job) (outcome, bool) {
	file, ok := insideLibrary(s.dataRoot, j.Library)
	if !ok {
		return outcome{Time: time.Now().Unix(), Result: "failed", Reason: "not a library path"}, false
	}
	args := []string{s.script, "--file", file, "--progress"}
	if j.AllowHardlinked {
		args = append(args, "--allow-hardlinked")
	}
	cmd := exec.Command(s.python, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return outcome{Time: time.Now().Unix(), Result: "failed", Reason: err.Error()}, false
	}

	s.mu.Lock()
	s.running = &progress{job: j, Started: time.Now().Unix()}
	s.mu.Unlock()
	if err := cmd.Start(); err != nil {
		return outcome{Time: time.Now().Unix(), Result: "failed", Reason: "audio-compat did not start: " + err.Error()}, false
	}

	var last event
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		e, ok := parseEvent(scanner.Bytes())
		if !ok {
			continue
		}
		last = e
		s.mu.Lock()
		switch e.Event {
		case "plan":
			s.running.Duration, s.running.Source = e.Duration, e.Source
		case "progress":
			s.running.Seconds, s.running.Duration = e.Seconds, e.Duration
			if e.Speed != nil {
				s.running.Speed = *e.Speed
			}
		}
		s.mu.Unlock()
	}
	err = cmd.Wait()

	now := time.Now().Unix()
	var exit *exec.ExitError
	switch {
	case err == nil && last.Event == "done":
		return outcome{Time: now, Result: "done"}, false
	case errors.As(err, &exit) && exit.ExitCode() == exitBusy:
		return outcome{}, true
	case last.Event == "failed":
		return outcome{Time: now, Result: "failed", Reason: last.Reason}, false
	}
	reason := strings.TrimSpace(stderr.String())
	if lines := strings.Split(reason, "\n"); len(lines) > 0 {
		reason = lines[len(lines)-1]
	}
	if reason == "" {
		reason = fmt.Sprintf("audio-compat ended without a result (%v)", err)
	}
	return outcome{Time: now, Result: "failed", Reason: reason}, false
}

func defaultStateFile(dir string) string { return filepath.Join(dir, "audio-convert.json") }
