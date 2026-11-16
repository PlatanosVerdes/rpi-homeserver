package main

import (
	"strings"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"defaults", nil, ""},
		{"a budget that is not a number", map[string]string{"DUB_BUDGET_GB": "lots"}, "DUB_BUDGET_GB"},
		{"a negative budget", map[string]string{"DUB_BUDGET_GB": "-1"}, "DUB_BUDGET_GB"},
		{"a URL without a host", map[string]string{"RADARR_URL": "radarr:7878"}, "RADARR_URL"},
		{"a qBittorrent URL without a scheme", map[string]string{"QBIT_URL": "qbittorrent"}, "QBIT_URL"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, key := range []string{"DUB_BUDGET_GB", "RADARR_URL", "PROWLARR_URL", "QBIT_URL", "SCAN_INTERVAL", "AUDIO_MANAGER_STATE"} {
				t.Setenv(key, c.env[key])
			}
			cfg, err := loadConfig()
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err %v, want one naming %s", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.reencode.ScanEvery != 6*time.Hour || cfg.dubs.Budget != 100<<30 || cfg.dubs.StateDir != "/state/add-audio" {
				t.Errorf("defaults: %+v", cfg)
			}
		})
	}
}

func TestLoadConfigTrimsTheArrURLs(t *testing.T) {
	t.Setenv("RADARR_URL", "http://radarr:7878/")
	t.Setenv("PROWLARR_URL", "http://prowlarr:9696/")
	t.Setenv("SCAN_INTERVAL", "90m")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.dubs.RadarrURL != "http://radarr:7878" || cfg.dubs.ProwlarrURL != "http://prowlarr:9696" || cfg.reencode.ScanEvery != 90*time.Minute {
		t.Errorf("%+v", cfg)
	}
}
