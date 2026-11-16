package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func postQbm(t *testing.T, body string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	qbmHook(recorder, httptest.NewRequest(http.MethodPost, "/qbit-manage", strings.NewReader(body)))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status %d", recorder.Code)
	}
}

func TestARunWithoutCountsIsAnErrorNotARun(t *testing.T) {
	qbmFile = filepath.Join(t.TempDir(), "qbit-manage.json")
	qbm = qbmState{}
	postQbm(t, `{"function":"run_end","run_time":"0:00:05","end_time":"2026-11-15 10:00:00",
		"next_run":"2026-11-15 11:00:00","torrents_deleted":2}`)
	ran := qbm.Received
	if ran == 0 || qbm.Interval != 3600 || qbm.Stats["torrents_deleted"] != 2.0 {
		t.Fatalf("a real run was not recorded: %+v", qbm)
	}

	postQbm(t, `{"function":"run_end","body":"Qbittorrent Error: only compatible with v5.2.4","run_time":"0:00:05"}`)
	if qbm.Received != ran || qbm.Errored == 0 || qbm.Interval != 3600 {
		t.Fatalf("an empty run counted as a run, or dropped the interval: %+v", qbm)
	}
}
