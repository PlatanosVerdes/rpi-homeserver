// audio-manager has two jobs. Re-encode (internal/reencode) converts a file whose audio a client had
// re-encoded, the way the night run does. Add audio (internal/dubs) fetches a dub for a library film
// in a language it lacks and adds the track.
package main

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"audio-manager/internal/dubs"
	"audio-manager/internal/httpx"
	"audio-manager/internal/reencode"
	"audio-manager/web"
)

const listenAddr = ":8088"

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// duration reads a positive duration such as 5m or 168h.
func duration(key string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s %q is not a duration such as 5m or 168h", key, raw)
	}
	return d, nil
}

type config struct {
	reencode reencode.Config
	dubs     dubs.Config
}

// loadConfig reads every setting from the environment, failing on a bad value.
func loadConfig() (config, error) {
	stateDir := env("AUDIO_MANAGER_STATE", "/state")
	dataRoot := env("DATA_ROOT", "/mnt/data")
	python := env("PYTHON", "python3")
	scanEvery := 6 * time.Hour
	if d, err := time.ParseDuration(os.Getenv("SCAN_INTERVAL")); err == nil && d > 0 {
		scanEvery = d
	}
	sweepEvery, err := duration("SWEEP_EVERY", 5*time.Minute)
	if err != nil {
		return config{}, err
	}
	sweepRefresh, err := duration("SWEEP_REFRESH", 7*24*time.Hour)
	if err != nil {
		return config{}, err
	}
	budgetGB, err := strconv.ParseFloat(env("DUB_BUDGET_GB", "100"), 64)
	if err != nil || budgetGB < 0 {
		return config{}, fmt.Errorf("DUB_BUDGET_GB %q is not a number of GB", os.Getenv("DUB_BUDGET_GB"))
	}
	cfg := config{
		reencode: reencode.Config{
			LedgerFile: env("AUDIO_RECODES", "/ledger/audio-recodes.json"),
			StateDir:   stateDir,
			DataRoot:   dataRoot,
			Python:     python,
			Script:     env("AUDIO_COMPAT", "/scripts/audio-compat.py"),
			ScanEvery:  scanEvery,
		},
		dubs: dubs.Config{
			RadarrURL:     strings.TrimRight(env("RADARR_URL", "http://radarr:7878"), "/"),
			RadarrKey:     os.Getenv("RADARR_API_KEY"),
			ProwlarrURL:   strings.TrimRight(env("PROWLARR_URL", "http://prowlarr:9696"), "/"),
			ProwlarrKey:   os.Getenv("PROWLARR_API_KEY"),
			QbitURL:       env("QBIT_URL", "http://qbittorrent:8080"),
			QbitUser:      os.Getenv("QBIT_USER"),
			QbitPassword:  os.Getenv("QBIT_PASSWORD"),
			RulesFile:     env("TRACKER_RULES", "/trackers/rules.json"),
			LanguagesFile: env("LANGUAGES_FILE", "/scripts/languages.json"),
			AddAudio:      env("ADD_AUDIO", "/scripts/add-audio.py"),
			Python:        python,
			DataRoot:      dataRoot,
			StateDir:      filepath.Join(stateDir, "add-audio"),
			Budget:        int64(budgetGB * (1 << 30)),
			TelegramToken: os.Getenv("TELEGRAM_ALERT_BOT_TOKEN"),
			TelegramChat:  os.Getenv("TELEGRAM_ALERT_CHAT_ID"),
			SweepEvery:    sweepEvery,
			SweepRefresh:  sweepRefresh,
		},
	}
	for name, value := range map[string]string{"RADARR_URL": cfg.dubs.RadarrURL, "PROWLARR_URL": cfg.dubs.ProwlarrURL, "QBIT_URL": cfg.dubs.QbitURL} {
		if u, err := url.Parse(value); err != nil || u.Scheme == "" || u.Host == "" {
			return config{}, fmt.Errorf("%s %q is not a URL", name, value)
		}
	}
	return cfg, nil
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("audio-manager: %v", err)
	}
	if err := os.MkdirAll(cfg.reencode.StateDir, 0o755); err != nil {
		log.Fatalf("audio-manager: %v", err)
	}
	recodes := reencode.New(cfg.reencode)
	adds, err := dubs.Open(cfg.dubs)
	if err != nil {
		log.Fatalf("audio-manager: %v", err)
	}

	http.Handle("/", web.Handler())
	http.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		payload := recodes.State()
		payload["add_audio"] = adds.Payload(r.URL.Query().Get("lang"))
		httpx.WriteJSON(w, http.StatusOK, payload)
	})
	http.HandleFunc("/api/play", recodes.ServePlay)
	http.HandleFunc("/api/cancel", recodes.ServeCancel)
	http.HandleFunc("/api/add-audio", adds.ServeAdd)
	http.HandleFunc("/api/add-audio/cancel", adds.ServeCancel)
	http.HandleFunc("/api/add-audio/check", adds.ServeCheck)
	http.HandleFunc("/api/add-audio/checks", adds.ServeChecks)

	recodes.Start()
	adds.Start()

	log.Printf("audio-manager: %s, ledger %s, library %s", listenAddr, cfg.reencode.LedgerFile, cfg.reencode.DataRoot)
	log.Fatal(http.ListenAndServe(listenAddr, nil))
}
