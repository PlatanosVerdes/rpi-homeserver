package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestUpdate(t *testing.T) {
	t0 := time.Unix(1000, 0)
	t1 := time.Unix(2000, 0)
	awake := update(diskState{Disk: "sda"}, fixture(t, "active.json"), t0)

	tests := []struct {
		name       string
		prev       diskState
		out        []byte
		wantOK     bool
		wantActive bool
		wantReport time.Time
	}{
		{"awake disk reads SMART", diskState{Disk: "sda"}, fixture(t, "active.json"), true, true, t1},
		{"sleeping disk keeps the last SMART read", awake, fixture(t, "standby.json"), true, false, t0},
		{"sleeping disk never seen awake has no SMART", diskState{Disk: "sda"}, fixture(t, "standby.json"), true, false, time.Time{}},
		{"unreadable disk is not ok and keeps its SMART", awake, fixture(t, "open-failed.json"), false, false, t0},
		{"garbage output is not ok", awake, []byte("not json"), false, false, t0},
		{"no output is not ok", diskState{Disk: "sda"}, nil, false, false, time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := update(tt.prev, tt.out, t1)
			if got.PollOK != tt.wantOK || got.Active != tt.wantActive || !got.ReportTime.Equal(tt.wantReport) {
				t.Errorf("got ok=%v active=%v report=%v, want ok=%v active=%v report=%v",
					got.PollOK, got.Active, got.ReportTime, tt.wantOK, tt.wantActive, tt.wantReport)
			}
		})
	}
}

func TestAsleep(t *testing.T) {
	for mode, want := range map[string]bool{
		"STANDBY": true, "STANDBY_Y": true, "SLEEP": true,
		"ACTIVE or IDLE": false, "IDLE_A": false, "": false,
	} {
		if got := asleep(mode); got != want {
			t.Errorf("asleep(%q) = %v, want %v", mode, got, want)
		}
	}
}

func TestWriteMetrics(t *testing.T) {
	awake := update(diskState{Disk: "sda"}, fixture(t, "active.json"), time.Unix(1000, 0))
	asleepDisk := update(awake, fixture(t, "standby.json"), time.Unix(2000, 0))
	asleepDisk.Disk = "sdb"
	failed := update(diskState{Disk: "sdc"}, fixture(t, "open-failed.json"), time.Unix(2000, 0))

	var b strings.Builder
	writeMetrics(&b, []diskState{awake, asleepDisk, failed}, time.Unix(2000, 0))
	out := b.String()

	for _, want := range []string{
		`disk_health_active{disk="sda"} 1`,
		`disk_health_active{disk="sdb"} 0`,
		`disk_health_poll_ok{disk="sdc"} 0`,
		`disk_health_info{disk="sda",model="ST1000DM010-2EP102",serial="TESTSERIAL1"} 1`,
		`disk_health_smart_passed{disk="sda"} 1`,
		`disk_health_temperature_celsius{disk="sda"} 37`,
		`disk_health_attribute_raw{disk="sda",id="5",name="Reallocated_Sector_Ct"} 0`,
		`disk_health_attribute_raw{disk="sda",id="4",name="Start_Stop_Count"} 32`,
		`disk_health_attribute_threshold{disk="sda",id="5",name="Reallocated_Sector_Ct"} 10`,
		`disk_health_smart_timestamp_seconds{disk="sdb"} 1000`,
		`disk_health_last_poll_timestamp_seconds 2000`,
	} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("missing %q", want)
		}
	}
	for _, unwanted := range []string{`disk_health_active{disk="sdc"}`, `disk_health_info{disk="sdc"`} {
		if strings.Contains(out, unwanted) {
			t.Errorf("unexpected %q", unwanted)
		}
	}
}

func TestListDisks(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"sda", "sdb", "sda1", "sdab", "mmcblk0", "zram0", "sg0"} {
		if err := os.WriteFile(dir+"/"+name, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := listDisks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "sda,sdab,sdb" {
		t.Errorf("got %v", got)
	}
}
