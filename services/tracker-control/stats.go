package main

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Reading what a private tracker says about the account, which is the only number that decides
// anything. The client's byte counters are not the account: they reset when a torrent is removed and
// know nothing about freeleech.
//
// What matters is not the ratio but the room left before the site acts on it:
//
//	buffer   = uploaded - min_ratio x downloaded
//	headroom = buffer / min_ratio        // GB of non-freeleech downloads that still fit
//
// Freeleech never moves `downloaded`, which is why headroom and not ratio decides whether the arrs
// may take a paid torrent. Hit & run is measured from the client rather than scraped, because
// torrents/info has the seed time and ratio before the site recomputes them.
//
// Credentials come from Prowlarr, which already holds them, so no secret is duplicated. The session
// cookie is kept on disk and only replaced when it stops working: these sites ban for hammering, and
// 48 logins a day for a number that moves in gigabytes is asking for it.

const userAgent = "Mozilla/5.0 (X11; Linux aarch64) AppleWebKit/537.36 (KHTML, like Gecko) " +
	"Chrome/126.0.0.0 Safari/537.36"

// A torrent in one of these is not announcing, so its seeding clock is not running.
var stoppedStates = map[string]bool{
	"pausedUP": true, "pausedDL": true, "stoppedUP": true, "stoppedDL": true,
	"error": true, "missingFiles": true, "unknown": true,
}

var units = map[string]float64{
	"B": 1, "KB": 1 << 10, "MB": 1 << 20, "GB": 1 << 30, "TB": 1 << 40, "PB": 1 << 50,
}

var (
	scriptTag = regexp.MustCompile(`(?s)<script.*?</script>`)
	anyTag    = regexp.MustCompile(`<[^>]+>`)
	sizeText  = regexp.MustCompile(`(?i)^([\d.,]+)\s*([KMGTP]?B)`)
	floatText = regexp.MustCompile(`^-?[\d.]+`)
)

type reading struct {
	Ratio          float64
	MinRatio       float64
	Uploaded       float64
	Downloaded     float64
	Buffer         float64
	Headroom       float64
	HnrPending     int
	HnrAtRisk      int
	WarningSeconds float64
}

// prowlarrCredentials reuses whatever Prowlarr already holds for the same site. One place for the
// password, not two.
func prowlarrCredentials(indexerName string) (user, password, token string, err error) {
	key, err := apiKey("prowlarr")
	if err != nil {
		return "", "", "", err
	}
	indexers, err := getJSON[[]map[string]any](prowlarrURL+"/api/v1/indexer", key)
	if err != nil {
		return "", "", "", err
	}
	for _, indexer := range indexers {
		if !strings.EqualFold(str(indexer, "name"), indexerName) {
			continue
		}
		for _, field := range objs(indexer, "fields") {
			switch str(field, "name") {
			case "username":
				user = str(field, "value")
			case "password":
				password = str(field, "value")
			case "alt2fatoken":
				token = str(field, "value")
			}
		}
		return user, password, token, nil
	}
	return "", "", "", fmt.Errorf("no indexer named %s in prowlarr", indexerName)
}

// siteClient keeps the session cookie on disk between runs, so a restart is not a new login.
func siteClient(tracker string, site *url.URL) (*http.Client, func(), error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, nil, err
	}
	path := filepath.Join(stateDir, tracker+"-cookies.json")
	if raw, err := os.ReadFile(path); err == nil {
		var stored []*http.Cookie
		if json.Unmarshal(raw, &stored) == nil {
			jar.SetCookies(site, stored)
		}
	}
	save := func() {
		if raw, err := json.Marshal(jar.Cookies(site)); err == nil {
			os.MkdirAll(stateDir, 0o755)
			os.WriteFile(path, raw, 0o600)
		}
	}
	return &http.Client{Jar: jar, Timeout: 30 * time.Second}, save, nil
}

// flatten: the profile is a table of label/value pairs, and every value sits on the line after its
// label once the markup is gone. Parsing that beats a selector per number on a site whose markup is
// rebuilt by JS.
func flatten(page string) []string {
	text := scriptTag.ReplaceAllString(page, " ")
	text = anyTag.ReplaceAllString(text, "\n")
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(html.UnescapeString(line)); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func valueAfter(lines []string, label string) string {
	wanted := strings.TrimSuffix(strings.ToLower(label), ":")
	for index, line := range lines {
		if strings.TrimSuffix(strings.ToLower(line), ":") == wanted && index+1 < len(lines) {
			return lines[index+1]
		}
	}
	return ""
}

func toBytes(text string) (float64, bool) {
	match := sizeText.FindStringSubmatch(strings.TrimSpace(text))
	if match == nil {
		return 0, false
	}
	value, err := strconv.ParseFloat(strings.ReplaceAll(match[1], ",", "."), 64)
	if err != nil {
		return 0, false
	}
	return value * units[strings.ToUpper(match[2])], true
}

func toFloat(text string) (float64, bool) {
	match := floatText.FindString(strings.ReplaceAll(strings.TrimSpace(text), ",", "."))
	if match == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(match, 64)
	return value, err == nil
}

type profile struct {
	uploaded, downloaded float64
	ratio, points        float64
	class, warnedUntil   string
}

// fetchProfile logs in only when the stored cookie has stopped working.
func fetchProfile(tracker string, config map[string]any) (profile, error) {
	var out profile
	user, password, token, err := prowlarrCredentials(withDefault(str(config, "prowlarr_indexer"), tracker))
	if err != nil {
		return out, err
	}
	if user == "" || password == "" {
		return out, fmt.Errorf("prowlarr holds no credentials for this site")
	}
	site := strings.TrimRight(str(config, "site"), "/")
	base, err := url.Parse(site)
	if err != nil {
		return out, err
	}
	client, save, err := siteClient(tracker, base)
	if err != nil {
		return out, err
	}
	profileURL := fmt.Sprintf("%s/profile/%s/view", site, url.PathEscape(user))

	read := func() string {
		request, err := http.NewRequest("GET", profileURL, nil)
		if err != nil {
			return ""
		}
		request.Header.Set("User-Agent", userAgent)
		request.Header.Set("Accept-Language", "en-US,en;q=0.9")
		resp, err := client.Do(request)
		if err != nil {
			return ""
		}
		defer resp.Body.Close()
		return readAll(resp)
	}

	page := read()
	if !strings.Contains(page, "account/logout") {
		form := url.Values{"username": {user}, "password": {password}, "alt2FAToken": {token}}
		request, err := http.NewRequest("POST", site+"/user/account/login/", strings.NewReader(form.Encode()))
		if err != nil {
			return out, err
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("User-Agent", userAgent)
		resp, err := client.Do(request)
		if err != nil {
			return out, err
		}
		landing := readAll(resp)
		resp.Body.Close()
		if !strings.Contains(landing, "account/logout") {
			return out, fmt.Errorf("login rejected")
		}
		save()
		page = read()
	}

	lines := flatten(page)
	uploaded, okUp := toBytes(valueAfter(lines, "uploaded"))
	downloaded, okDown := toBytes(valueAfter(lines, "downloaded"))
	if !okUp || !okDown {
		return out, fmt.Errorf("logged in but the profile held no byte counters")
	}
	out.uploaded, out.downloaded = uploaded, downloaded
	out.ratio, _ = toFloat(valueAfter(lines, "ratio"))
	out.points, _ = toFloat(valueAfter(lines, "TL Points"))
	out.class = valueAfter(lines, "Class")
	out.warnedUntil = valueAfter(lines, "Warned until")
	return out, nil
}

type owed struct {
	name      string
	hoursLeft float64
	ratio     float64
	stopped   bool
}

// hitAndRun is per torrent, not per account: one uncleared torrent is a warning, three is a disabled
// account. What is owed is the hours still to go on the torrents that have neither the seed time nor
// the ratio. Three parts of the rule differ per site: starts_at_progress (when the obligation
// begins), grace_hours (added to what is required, since the local counter runs through them) and
// min_ratio (null where only time clears anything).
//
// Exemptions are deliberately not implemented. They only ever reduce what is owed, and owing more
// than the site thinks is the safe direction for a number whose job is to prevent a ban.
func hitAndRun(config map[string]any, torrents []map[string]any) ([]owed, float64) {
	rule := obj(config, "hit_and_run")
	minHours := numOr(rule, "min_seed_hours", 240) + num(rule, "grace_hours")
	minRatio, hasRatio := rule["min_ratio"].(float64)
	startsAt := numOr(rule, "starts_at_progress", 1)
	hosts := hostSet(config)

	var pending []owed
	var worst float64
	for _, torrent := range torrents {
		if !hosts[announceHost(torrent)] {
			continue
		}
		if num(torrent, "progress") < startsAt {
			continue
		}
		hours := num(torrent, "seeding_time") / 3600
		if hours >= minHours || (hasRatio && num(torrent, "ratio") >= minRatio) {
			continue
		}
		left := minHours - hours
		// Owing hours is normal. Owing them while the clock is stopped is what gets an account
		// disabled: the site counts seeding time from announces, and a paused or broken torrent
		// does not announce.
		pending = append(pending, owed{orQ(str(torrent, "name")), left, num(torrent, "ratio"),
			stoppedStates[str(torrent, "state")]})
		if left > worst {
			worst = left
		}
	}
	return pending, worst
}

type clientSide struct {
	torrents, seeding int
	bytesOnDisk       float64
	uploadedBytes     float64
	leechBonusPercent float64
}

// clientSide is what the client knows per tracker, which is every tracker and not only the ones that
// log in: three of the five accounts cannot be read at all. None of it is the tracker's own
// accounting, hence source="client".
func clientNumbers(config map[string]any, torrents []map[string]any) clientSide {
	hosts := hostSet(config)
	var out clientSide
	var bonusGB float64
	for _, torrent := range torrents {
		if !hosts[announceHost(torrent)] {
			continue
		}
		out.torrents++
		out.bytesOnDisk += num(torrent, "size")
		out.uploadedBytes += num(torrent, "uploaded")
		if num(torrent, "progress") >= 1 {
			out.seeding++
			bonusGB += earnedBonus(torrent) / (1 << 30)
		}
	}
	out.leechBonusPercent = bonusGB / 10
	if out.leechBonusPercent > 100 {
		out.leechBonusPercent = 100
	}
	return out
}

// DigitalCore's published formula: only 50 GiB per torrent counts, scaled by 1 + (1/seeders) so
// being the only seeder of something scarce pays double.
func earnedBonus(torrent map[string]any) float64 {
	counted := num(torrent, "size")
	if capped := float64(50 << 30); counted > capped {
		counted = capped
	}
	seeders := num(torrent, "num_complete")
	if seeders < 1 {
		seeders = 1
	}
	return counted * (1 + 1/seeders)
}

// read does one pass over every tracker: the metric lines, the numbers control acts on, and whatever
// failed. The numbers used to be written to disk for a second cron job to pick up; they are returned
// in memory now, and still written for debugging.
func read(rules map[string]map[string]any) ([]string, map[string]reading, []string) {
	var problems []string
	torrents, err := qbitTorrents()
	if err != nil {
		problems = append(problems, fmt.Sprintf("qbittorrent: %v", err))
		torrents = nil
	}

	lines := []string{
		"# HELP tracker_up 1 when the site answered with the account page",
		"# TYPE tracker_up gauge",
		"# HELP tracker_ratio The ratio the site itself reports",
		"# TYPE tracker_ratio gauge",
		"# HELP tracker_min_ratio The ratio under which this site disables the account",
		"# TYPE tracker_min_ratio gauge",
		"# HELP tracker_uploaded_bytes Uploaded, as counted by the site",
		"# TYPE tracker_uploaded_bytes gauge",
		"# HELP tracker_downloaded_bytes Downloaded, as counted by the site (freeleech excluded)",
		"# TYPE tracker_downloaded_bytes gauge",
		"# HELP tracker_buffer_bytes uploaded - min_ratio x downloaded",
		"# TYPE tracker_buffer_bytes gauge",
		"# HELP tracker_headroom_bytes Non-freeleech downloads that still fit above the line",
		"# TYPE tracker_headroom_bytes gauge",
		"# HELP tracker_points Bonus points the site grants for seeding",
		"# TYPE tracker_points gauge",
		"# HELP tracker_warning_seconds Seconds left on an active warning, 0 when there is none",
		"# TYPE tracker_warning_seconds gauge",
		"# HELP tracker_hnr_pending Torrents owing a hit & run obligation right now",
		"# TYPE tracker_hnr_pending gauge",
		"# HELP tracker_hnr_hours_worst Seeding hours the furthest-behind torrent still owes",
		"# TYPE tracker_hnr_hours_worst gauge",
		"# HELP tracker_hnr_at_risk Torrents owing hours whose seeding clock is not running",
		"# TYPE tracker_hnr_at_risk gauge",
		"# HELP tracker_last_run_timestamp When this last read the site",
		"# TYPE tracker_last_run_timestamp gauge",
		"# HELP tracker_client_torrents Torrents in the client for this tracker",
		"# TYPE tracker_client_torrents gauge",
		"# HELP tracker_client_seeding Of those, how many are complete and seeding",
		"# TYPE tracker_client_seeding gauge",
		"# HELP tracker_client_bytes_on_disk What this tracker's torrents occupy locally",
		"# TYPE tracker_client_bytes_on_disk gauge",
		"# HELP tracker_client_uploaded_bytes Uploaded per the client, which is not the site's count",
		"# TYPE tracker_client_uploaded_bytes gauge",
		"# HELP tracker_leech_bonus_percent Estimated leech bonus from what is seeding, DigitalCore's formula",
		"# TYPE tracker_leech_bonus_percent gauge",
		"# HELP tracker_read_ratio Ratio read off the site by eye, for accounts that cannot be scraped",
		"# TYPE tracker_read_ratio gauge",
		"# HELP tracker_read_uploaded_bytes Uploaded, read off the site by eye",
		"# TYPE tracker_read_uploaded_bytes gauge",
		"# HELP tracker_read_downloaded_bytes Downloaded, read off the site by eye",
		"# TYPE tracker_read_downloaded_bytes gauge",
		"# HELP tracker_read_headroom_bytes Non-freeleech downloads that fit, from the read figures",
		"# TYPE tracker_read_headroom_bytes gauge",
		"# HELP tracker_read_points Bonus points, read off the site by eye",
		"# TYPE tracker_read_points gauge",
		"# HELP tracker_read_hnr Hit and run count the site itself shows",
		"# TYPE tracker_read_hnr gauge",
		"# HELP tracker_read_timestamp When those figures were read",
		"# TYPE tracker_read_timestamp gauge",
		"# HELP tracker_deadline_seconds Seconds left on a deadline the site imposes, 0 when none",
		"# TYPE tracker_deadline_seconds gauge",
	}
	detail := []string{
		"# HELP tracker_hnr_torrent_hours_left Hours of seeding a torrent still owes",
		"# TYPE tracker_hnr_torrent_hours_left gauge",
	}

	byEye := manualReadings()
	numbers := map[string]reading{}

	for _, tracker := range sortedKeys(rules) {
		config := rules[tracker]
		label := fmt.Sprintf("tracker=%q", escape(tracker))
		minRatio := numOr(config, "min_ratio", 0.4)
		pending, worst := hitAndRun(config, torrents)
		atRisk := 0
		for _, row := range pending {
			if row.stopped {
				atRisk++
			}
		}
		sort.Slice(pending, func(i, j int) bool { return pending[i].hoursLeft > pending[j].hoursLeft })
		rows := pending
		if len(rows) > 25 {
			rows = rows[:25]
		}

		// Every tracker gets these, readable account or not.
		client := clientNumbers(config, torrents)
		lines = append(lines,
			fmt.Sprintf("tracker_client_torrents{%s} %d", label, client.torrents),
			fmt.Sprintf("tracker_client_seeding{%s} %d", label, client.seeding),
			fmt.Sprintf("tracker_client_bytes_on_disk{%s} %.0f", label, client.bytesOnDisk),
			fmt.Sprintf("tracker_client_uploaded_bytes{%s} %.0f", label, client.uploadedBytes))
		// Only where the mechanic exists: on a site with no leech bonus the number is noise.
		if len(obj(config, "leech_bonus")) > 0 {
			lines = append(lines, fmt.Sprintf("tracker_leech_bonus_percent{%s} %.1f", label, client.leechBonusPercent))
		}

		if eye, ok := byEye[tracker]; ok {
			up, hasUp := eye["uploaded_gb"].(float64)
			down, hasDown := eye["downloaded_gb"].(float64)
			if hasUp && hasDown {
				lines = append(lines,
					fmt.Sprintf("tracker_read_uploaded_bytes{%s} %.0f", label, up*(1<<30)),
					fmt.Sprintf("tracker_read_downloaded_bytes{%s} %.0f", label, down*(1<<30)))
				if minRatio > 0 {
					bufferRead := (up - minRatio*down) * (1 << 30)
					lines = append(lines, fmt.Sprintf("tracker_read_headroom_bytes{%s} %.0f", label, bufferRead/minRatio))
				}
			}
			for _, pair := range []struct{ key, metric string }{
				{"ratio", "tracker_read_ratio"}, {"points", "tracker_read_points"},
				{"hit_and_run", "tracker_read_hnr"},
			} {
				if value, ok := eye[pair.key].(float64); ok {
					lines = append(lines, fmt.Sprintf("%s{%s} %g", pair.metric, label, value))
				}
			}
			if at, ok := eye["read_at"].(string); ok && at != "" {
				if parsed, err := parseDay(at); err == nil {
					lines = append(lines, fmt.Sprintf("tracker_read_timestamp{%s} %d", label, parsed.Unix()))
				} else {
					problems = append(problems, fmt.Sprintf("%s: unreadable read_at %s", tracker, at))
				}
			}
			if at, ok := eye["deadline"].(string); ok && at != "" {
				if parsed, err := parseDay(at); err == nil {
					left := time.Until(parsed).Seconds()
					if left < 0 {
						left = 0
					}
					lines = append(lines, fmt.Sprintf("tracker_deadline_seconds{%s} %.0f", label, left))
				} else {
					problems = append(problems, fmt.Sprintf("%s: unreadable deadline %s", tracker, at))
				}
			}
		}

		// A site nobody can log into still has torrents in the client, and the hit & run clock is
		// the half that gets accounts banned. So obligations are measured for every tracker in the
		// file, and only the account numbers need a `site`.
		if str(config, "site") == "" {
			// A site with no ratio rule gets no line and no headroom: emitting zero for both puts a
			// tracker that has no threshold at the top of a "tightest headroom" panel.
			if minRatio > 0 {
				lines = append(lines, fmt.Sprintf("tracker_min_ratio{%s} %g", label, minRatio))
			}
			lines = append(lines,
				fmt.Sprintf("tracker_hnr_pending{%s} %d", label, len(pending)),
				fmt.Sprintf("tracker_hnr_hours_worst{%s} %.1f", label, worst),
				fmt.Sprintf("tracker_hnr_at_risk{%s} %d", label, atRisk))
			detail = append(detail, hnrDetail(label, rows)...)
			continue
		}

		stats, err := fetchProfile(tracker, config)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", tracker, err))
			lines = append(lines, fmt.Sprintf("tracker_up{%s} 0", label))
			continue
		}

		buffer := stats.uploaded - minRatio*stats.downloaded
		warning := warningSeconds(stats.warnedUntil)
		lines = append(lines,
			fmt.Sprintf("tracker_up{%s} 1", label),
			fmt.Sprintf("tracker_ratio{%s} %g", label, stats.ratio),
			fmt.Sprintf("tracker_min_ratio{%s} %g", label, minRatio),
			fmt.Sprintf("tracker_uploaded_bytes{%s} %.0f", label, stats.uploaded),
			fmt.Sprintf("tracker_downloaded_bytes{%s} %.0f", label, stats.downloaded),
			fmt.Sprintf("tracker_buffer_bytes{%s} %.0f", label, buffer),
			fmt.Sprintf("tracker_headroom_bytes{%s} %.0f", label, buffer/minRatio),
			fmt.Sprintf("tracker_points{%s} %g", label, stats.points),
			fmt.Sprintf("tracker_warning_seconds{%s} %.0f", label, warning),
			fmt.Sprintf("tracker_hnr_pending{%s} %d", label, len(pending)),
			fmt.Sprintf("tracker_hnr_hours_worst{%s} %.1f", label, worst),
			fmt.Sprintf("tracker_hnr_at_risk{%s} %d", label, atRisk),
			fmt.Sprintf("tracker_class_info{%s,class=%q} 1", label, escape(withDefault(stats.class, "?"))))
		detail = append(detail, hnrDetail(label, rows)...)

		numbers[tracker] = reading{
			Ratio: stats.ratio, MinRatio: minRatio,
			Uploaded: stats.uploaded, Downloaded: stats.downloaded,
			Buffer: buffer, Headroom: buffer / minRatio,
			HnrPending: len(pending), HnrAtRisk: atRisk, WarningSeconds: warning,
		}
	}

	lines = append(lines, fmt.Sprintf("tracker_last_run_timestamp %d", time.Now().Unix()))
	saveState(numbers)
	return append(lines, detail...), numbers, problems
}

func hnrDetail(label string, rows []owed) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		seeding := "yes"
		if row.stopped {
			seeding = "no"
		}
		out = append(out, fmt.Sprintf("tracker_hnr_torrent_hours_left{%s,name=%q,ratio=\"%.2f\",seeding=%q} %.1f",
			label, cut(row.name, 90), row.ratio, seeding, row.hoursLeft))
	}
	return out
}

// `Warned until 2026-09-03 20:25:46`, in the site's own timezone, which it reports as UTC.
func warningSeconds(text string) float64 {
	text = strings.TrimSpace(text)
	if len(text) < 19 {
		return 0
	}
	when, err := time.Parse("2006-01-02 15:04:05", text[:19])
	if err != nil {
		return 0
	}
	left := time.Until(when.UTC()).Seconds()
	if left < 0 {
		return 0
	}
	return left
}

// Numbers read off a site by eye, because three of these accounts cannot be read any other way.
// Kept in git with the date they were read, so a panel can show both the figure and how stale it is:
// a number with no timestamp is worse than no number.
func manualReadings() map[string]map[string]any {
	path := env("TRACKER_READINGS", "/config/readings.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var all map[string]any
	if json.Unmarshal(raw, &all) != nil {
		return nil
	}
	out := map[string]map[string]any{}
	for name, value := range all {
		if strings.HasPrefix(name, "_") {
			continue
		}
		if row, ok := value.(map[string]any); ok {
			out[name] = row
		}
	}
	return out
}

func saveState(numbers map[string]reading) {
	payload := map[string]any{"read_at": time.Now().Unix(), "trackers": numbers}
	if raw, err := json.MarshalIndent(payload, "", "  "); err == nil {
		os.MkdirAll(stateDir, 0o755)
		os.WriteFile(filepath.Join(stateDir, "state.json"), append(raw, '\n'), 0o644)
	}
}

func parseDay(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if len(value) == 10 {
		return time.Parse("2006-01-02", value)
	}
	return time.Parse(time.RFC3339, strings.Replace(value, " ", "T", 1))
}
