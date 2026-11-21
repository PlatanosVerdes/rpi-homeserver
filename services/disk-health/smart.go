package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

type report struct {
	Smartctl struct {
		ExitStatus int `json:"exit_status"`
	} `json:"smartctl"`
	PowerMode *struct {
		Name string `json:"name"`
	} `json:"power_mode"`
	ModelName    string `json:"model_name"`
	SerialNumber string `json:"serial_number"`
	SmartStatus  *struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`
	Temperature *struct {
		Current float64 `json:"current"`
	} `json:"temperature"`
	Attributes struct {
		Table []attribute `json:"table"`
	} `json:"ata_smart_attributes"`
}

type attribute struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Value  int    `json:"value"`
	Thresh int    `json:"thresh"`
	Raw    struct {
		Value float64 `json:"value"`
	} `json:"raw"`
}

type diskState struct {
	Disk       string
	PollOK     bool
	Active     bool
	ExitStatus int
	Report     *report
	ReportTime time.Time
}

var diskName = regexp.MustCompile(`^sd[a-z]+$`)

func listDisks(devDir string) ([]string, error) {
	entries, err := os.ReadDir(devDir)
	if err != nil {
		return nil, err
	}
	var disks []string
	for _, e := range entries {
		if diskName.MatchString(e.Name()) {
			disks = append(disks, e.Name())
		}
	}
	sort.Strings(disks)
	return disks, nil
}

func asleep(powerMode string) bool {
	return strings.HasPrefix(powerMode, "STANDBY") || powerMode == "SLEEP"
}

// update folds one smartctl answer into the previous state. A sleeping disk only reports that it
// sleeps, so its SMART values stay those of the last awake poll.
func update(prev diskState, out []byte, now time.Time) diskState {
	next := diskState{Disk: prev.Disk, Report: prev.Report, ReportTime: prev.ReportTime, ExitStatus: prev.ExitStatus}
	var r report
	if err := json.Unmarshal(out, &r); err != nil || r.PowerMode == nil {
		return next
	}
	next.PollOK = true
	if asleep(r.PowerMode.Name) {
		return next
	}
	next.Active = true
	next.ExitStatus = r.Smartctl.ExitStatus
	if r.ModelName != "" {
		next.Report, next.ReportTime = &r, now
	}
	return next
}

var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

func label(s string) string { return labelEscaper.Replace(s) }

func boolValue(b bool) int {
	if b {
		return 1
	}
	return 0
}

type metric struct{ name, typ, help string }

var metrics = []metric{
	{"disk_health_poll_ok", "gauge", "1 if the last smartctl poll of the disk answered"},
	{"disk_health_active", "gauge", "1 if the disk is spinning (ACTIVE or IDLE), 0 if in STANDBY or SLEEP"},
	{"disk_health_smartctl_exit_status", "gauge", "smartctl exit status bitmask from the last awake poll"},
	{"disk_health_info", "gauge", "Disk identity"},
	{"disk_health_smart_passed", "gauge", "1 if the SMART overall-health self-assessment passed"},
	{"disk_health_temperature_celsius", "gauge", "Current drive temperature"},
	{"disk_health_attribute_raw", "gauge", "SMART attribute raw value"},
	{"disk_health_attribute_normalized", "gauge", "SMART attribute normalized value"},
	{"disk_health_attribute_threshold", "gauge", "SMART attribute failure threshold"},
	{"disk_health_smart_timestamp_seconds", "gauge", "When the SMART values were last read"},
	{"disk_health_last_poll_timestamp_seconds", "gauge", "When the last poll of all disks finished"},
}

func writeMetrics(w io.Writer, states []diskState, lastPoll time.Time) {
	for _, m := range metrics {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", m.name, m.help, m.name, m.typ)
	}
	fmt.Fprintf(w, "disk_health_last_poll_timestamp_seconds %d\n", lastPoll.Unix())
	for _, s := range states {
		d := label(s.Disk)
		fmt.Fprintf(w, "disk_health_poll_ok{disk=\"%s\"} %d\n", d, boolValue(s.PollOK))
		if s.PollOK {
			fmt.Fprintf(w, "disk_health_active{disk=\"%s\"} %d\n", d, boolValue(s.Active))
		}
		r := s.Report
		if r == nil {
			continue
		}
		fmt.Fprintf(w, "disk_health_smartctl_exit_status{disk=\"%s\"} %d\n", d, s.ExitStatus)
		fmt.Fprintf(w, "disk_health_info{disk=\"%s\",model=\"%s\",serial=\"%s\"} 1\n", d, label(r.ModelName), label(r.SerialNumber))
		fmt.Fprintf(w, "disk_health_smart_timestamp_seconds{disk=\"%s\"} %d\n", d, s.ReportTime.Unix())
		if r.SmartStatus != nil {
			fmt.Fprintf(w, "disk_health_smart_passed{disk=\"%s\"} %d\n", d, boolValue(r.SmartStatus.Passed))
		}
		if r.Temperature != nil {
			fmt.Fprintf(w, "disk_health_temperature_celsius{disk=\"%s\"} %g\n", d, r.Temperature.Current)
		}
		for _, a := range r.Attributes.Table {
			l := fmt.Sprintf(`disk="%s",id="%d",name="%s"`, d, a.ID, label(a.Name))
			fmt.Fprintf(w, "disk_health_attribute_raw{%s} %.0f\n", l, a.Raw.Value)
			fmt.Fprintf(w, "disk_health_attribute_normalized{%s} %d\n", l, a.Value)
			fmt.Fprintf(w, "disk_health_attribute_threshold{%s} %d\n", l, a.Thresh)
		}
	}
}
