package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// qbit-manage publishes no metrics, and the answer its own docs give for "did it run" is a Discord
// or Apprise message per run: something that cannot tell you about silence, which is the only
// failure that matters here. If it dies, no share limits are written, nothing is tagged, nothing is
// deleted, and every panel keeps showing the state it left behind.
//
// What it does have is a run_end webhook, and that carries the whole run rather than a heartbeat.
// So it posts here and this turns it into the shape the other collectors already use: when it last
// finished, and how long it is supposed to be between runs, so "overdue" needs no threshold written
// anywhere. The stats ride along, because they are the tool's own accounting of what it did and
// nothing else in this repo has them.
//
// The timestamp is when the POST arrived, not the end_time in the payload: that one is formatted in
// the tool's local time with no offset, and a freshness alert should not depend on two containers
// agreeing about a timezone. The interval is `next_run - end_time`, a difference between two strings
// from the same clock, so it is right whatever that clock is.

type qbmState struct {
	Received int64          `json:"received"`
	Interval int64          `json:"interval_seconds"`
	RunTime  float64        `json:"run_seconds"`
	Stats    map[string]any `json:"stats"`
	Errored  int64          `json:"last_error"`
}

var (
	qbmMu    sync.Mutex
	qbm      qbmState
	qbmFile  = filepath.Join(env("STATE_PATH", "/state"), "qbit-manage.json")
	qbmClock = "2006-01-02 15:04:05"
)

func qbmLoad() {
	raw, err := os.ReadFile(qbmFile)
	if err != nil {
		return
	}
	qbmMu.Lock()
	defer qbmMu.Unlock()
	if err := json.Unmarshal(raw, &qbm); err != nil {
		log.Printf("qbit-manage state: %v", err)
	}
}

func qbmSave() {
	raw, err := json.Marshal(qbm)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(qbmFile), 0o755); err != nil {
		log.Printf("qbit-manage state: %v", err)
		return
	}
	if err := os.WriteFile(qbmFile, raw, 0o644); err != nil {
		log.Printf("qbit-manage state: %v", err)
	}
}

// qbmHook takes both hooks qbit-manage is pointed at: run_end, and error.
func qbmHook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		log.Printf("qbit-manage hook: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	now := time.Now().Unix()

	qbmMu.Lock()
	switch str(payload, "function") {
	case "error":
		qbm.Errored = now
		log.Printf("qbit-manage reported an error: %s", cut(str(payload, "body"), 200))
	default:
		qbm.Received = now
		qbm.RunTime = qbmDuration(str(payload, "run_time"))
		qbm.Interval = qbmInterval(str(payload, "end_time"), str(payload, "next_run"))
		qbm.Stats = map[string]any{}
		for key, value := range payload {
			// Every number it reports, without naming them here: a counter added upstream shows up
			// without a code change.
			if number, ok := value.(float64); ok {
				qbm.Stats[key] = number
			}
		}
	}
	qbmSave()
	qbmMu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

// "0:00:09", and occasionally "1 day, 0:00:09".
func qbmDuration(text string) float64 {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	var seconds float64
	if before, after, found := strings.Cut(text, ","); found {
		var days float64
		fmt.Sscanf(strings.TrimSpace(before), "%f", &days)
		seconds += days * 86400
		text = strings.TrimSpace(after)
	}
	var clock float64
	for _, part := range strings.Split(text, ":") {
		value, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return seconds
		}
		clock = clock*60 + value
	}
	return seconds + clock
}

// Both strings come from the tool's own clock, so their difference needs no timezone.
func qbmInterval(end, next string) int64 {
	if end == "" || next == "" {
		return 0
	}
	endAt, err := time.Parse(qbmClock, end)
	if err != nil {
		return 0
	}
	nextAt, err := time.Parse(qbmClock, next)
	if err != nil {
		return 0
	}
	seconds := int64(nextAt.Sub(endAt).Seconds())
	if seconds < 0 {
		return 0
	}
	return seconds
}

// qbmLines exposes nothing at all until the first run has been received: a zero here would read as
// "last ran in 1970" and fire the overdue alert on a fresh container.
func qbmLines() []string {
	qbmMu.Lock()
	current := qbm
	qbmMu.Unlock()
	if current.Received == 0 && current.Errored == 0 {
		return nil
	}

	lines := []string{
		"# HELP qbit_manage_last_run_timestamp_seconds When qbit-manage last finished a run",
		"# TYPE qbit_manage_last_run_timestamp_seconds gauge",
		"# HELP qbit_manage_interval_seconds How long it says there is until its next run",
		"# TYPE qbit_manage_interval_seconds gauge",
		"# HELP qbit_manage_run_seconds How long its last run took",
		"# TYPE qbit_manage_run_seconds gauge",
		"# HELP qbit_manage_last_error_timestamp_seconds When it last reported an error, absent if never",
		"# TYPE qbit_manage_last_error_timestamp_seconds gauge",
		"# HELP qbit_manage_last_run_stat What that run actually did, as the tool counts it",
		"# TYPE qbit_manage_last_run_stat gauge",
	}
	if current.Received > 0 {
		lines = append(lines,
			fmt.Sprintf("qbit_manage_last_run_timestamp_seconds %d", current.Received),
			fmt.Sprintf("qbit_manage_interval_seconds %d", current.Interval),
			fmt.Sprintf("qbit_manage_run_seconds %.0f", current.RunTime))
	}
	if current.Errored > 0 {
		lines = append(lines, fmt.Sprintf("qbit_manage_last_error_timestamp_seconds %d", current.Errored))
	}
	keys := make([]string, 0, len(current.Stats))
	for key := range current.Stats {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if number, ok := current.Stats[key].(float64); ok {
			lines = append(lines, fmt.Sprintf("qbit_manage_last_run_stat{stat=%q} %g", escape(key), number))
		}
	}
	return lines
}
