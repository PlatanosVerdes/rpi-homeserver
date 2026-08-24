package main

import (
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// What the existing exporters cannot answer, which is always "which one":
//
//	arr_quality_change_*      which film was upgraded, from which quality to which
//	qbit_torrent_*            which torrents are in each state, not how many
//	arr_indexer_grabs_90d     which indexers actually get used
//	arr_media_size_bytes      where the disk went: size per title, tagged with its quality
//	arr_library_titles        how many films and series there are, on disk and in total
//	arr_media_audio           which audio tracks each film has, one series per language
//	arr_waiting               everything Radarr waits for: downloading, missing or below cutoff
//	prowlarr_indexer_*        up, and separately whether anything is being asked of it
//	maintainerr_pending_*     which films are watched and waiting out the grace period
//	arr_orphan_*              what nothing is managing at all
//
// Labels carry identity here, which is not what Prometheus is for. The trade is deliberate: the
// alternative is Grafana's Infinity datasource querying each API live, which means a plugin plus
// every app's API key stored in Grafana. Each group is bounded to tens of series.
const (
	keepUpgrades   = 25
	pairWindow     = 120 * time.Second // between the delete and the import, to call it one upgrade
	minOrphanBytes = 100 << 20         // below this it is a sample, an nfo or a stray subtitle
	nameLimit      = 90
)

type arr struct {
	name         string
	url          string
	api          string
	deletedEvent string
	extra        string
	titleOf      func(map[string]any) string
}

func arrs() []arr {
	return []arr{
		{"radarr", radarrURL, "v3", "movieFileDeleted", "includeMovie=true",
			func(r map[string]any) string { return orQ(str(obj(r, "movie"), "title")) }},
		{"sonarr", sonarrURL, "v3", "episodeFileDeleted", "includeSeries=true&includeEpisode=true",
			func(r map[string]any) string { return orQ(str(obj(r, "series"), "title")) }},
	}
}

func orQ(value string) string {
	if value == "" {
		return "?"
	}
	return value
}

// media runs every group. A group that fails is named in the returned problems and the others still
// produce their lines.
func media() ([]string, []string) {
	var lines, problems []string
	for _, group := range []func() ([]string, []string){
		qualityChanges, qbitMetrics, indexerUsage, librarySizes, mediaAudio,
		waitingOn, prowlarrIndexers, prowlarrActivity, maintainerrPending, orphans,
	} {
		part, failed := group()
		lines = append(lines, part...)
		problems = append(problems, failed...)
	}
	return lines, problems
}

// An upgrade is two events at the same instant: the old file deleted, the new one imported.
func qualityChanges() ([]string, []string) {
	type change struct {
		app, title, from, to string
		at                   time.Time
	}
	var rows []change
	var problems []string

	for _, app := range arrs() {
		key, err := apiKey(app.name)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s history: %v", app.name, err))
			continue
		}
		records, err := list(fmt.Sprintf("%s/api/%s/history?pageSize=200&sortKey=date&sortDirection=descending&%s",
			app.url, app.api, app.extra), key)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s history: %v", app.name, err))
			continue
		}
		var imported []map[string]any
		for _, record := range records {
			if str(record, "eventType") == "downloadFolderImported" {
				imported = append(imported, record)
			}
		}
		for _, record := range records {
			if str(record, "eventType") != app.deletedEvent {
				continue
			}
			if str(obj(record, "data"), "reason") != "Upgrade" {
				continue
			}
			at, err := when(str(record, "date"))
			if err != nil {
				continue
			}
			for _, candidate := range imported {
				if app.titleOf(candidate) != app.titleOf(record) {
					continue
				}
				other, err := when(str(candidate, "date"))
				if err != nil {
					continue
				}
				if other.Sub(at).Abs() > pairWindow {
					continue
				}
				rows = append(rows, change{app.name, app.titleOf(record), quality(record), quality(candidate), at})
				break
			}
		}
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].at.After(rows[j].at) })
	if len(rows) > keepUpgrades {
		rows = rows[:keepUpgrades]
	}

	lines := []string{
		"# HELP arr_quality_change_timestamp Unix time a file was replaced by a different quality",
		"# TYPE arr_quality_change_timestamp gauge",
	}
	for _, row := range rows {
		// The title is not cut here, unlike everywhere else: this is the one label a person reads
		// as a sentence, and the group is capped at 25 rows anyway.
		lines = append(lines, fmt.Sprintf(
			"arr_quality_change_timestamp{app=%q,title=%q,from=%q,to=%q} %d",
			row.app, escape(row.title), escape(row.from), escape(row.to), row.at.Unix()))
	}
	lines = append(lines,
		"# HELP arr_quality_changes_tracked How many upgrades are being exposed",
		"# TYPE arr_quality_changes_tracked gauge",
		fmt.Sprintf("arr_quality_changes_tracked %d", len(rows)))
	return lines, problems
}

func qbitMetrics() ([]string, []string) {
	items, err := qbitTorrents()
	if err != nil {
		return nil, []string{fmt.Sprintf("qbittorrent: %v", err)}
	}

	progress := []string{
		"# HELP qbit_torrent_progress Download progress 0-1, name and state as labels",
		"# TYPE qbit_torrent_progress gauge",
	}
	ratio := []string{"# HELP qbit_torrent_ratio Share ratio per torrent", "# TYPE qbit_torrent_ratio gauge"}
	size := []string{"# HELP qbit_torrent_size_bytes Total size per torrent", "# TYPE qbit_torrent_size_bytes gauge"}
	for _, torrent := range items {
		category := str(torrent, "category")
		if category == "" {
			category = "none"
		}
		labels := fmt.Sprintf("name=%q,state=%q,category=%q",
			cut(str(torrent, "name"), nameLimit), escape(str(torrent, "state")), escape(category))
		progress = append(progress, fmt.Sprintf("qbit_torrent_progress{%s} %g", labels, num(torrent, "progress")))
		// Three decimals, trailing zeros trimmed: the same text the Pushgateway used to carry, so a
		// diff against it stays readable.
		ratio = append(ratio, fmt.Sprintf("qbit_torrent_ratio{%s} %s", labels,
			strconv.FormatFloat(math.Round(num(torrent, "ratio")*1000)/1000, 'f', -1, 64)))
		size = append(size, fmt.Sprintf("qbit_torrent_size_bytes{%s} %.0f", labels, num(torrent, "size")))
	}

	// Every seed goal carries margin over what a site asks, because qBittorrent's clock counts while
	// announces are being rejected and the tracker's does not. That gap only opens when announces
	// fail, and qBittorrent already knows: `tracker` holds the first working one and is empty when
	// none of them is. So watch the cause instead of guessing the margin per site.
	silent := []string{
		"# HELP qbit_torrent_no_working_tracker Seeding whose announces are not landing",
		"# TYPE qbit_torrent_no_working_tracker gauge",
	}
	for _, torrent := range items {
		if strings.TrimSpace(str(torrent, "tracker")) == "" {
			silent = append(silent, fmt.Sprintf("qbit_torrent_no_working_tracker{name=%q} 1",
				cut(str(torrent, "name"), nameLimit)))
		}
	}

	// A torrent with no category can never be tagged noHL, because qbit-manage only inspects the
	// categories in its nohardlinks list, and without autobrr's `ratio` tag no share_limits group
	// matches it either. So it seeds forever, nothing reclaims it and nothing says so.
	unmanaged := []string{
		"# HELP qbit_torrent_unmanaged Torrent that no retention rule can ever match",
		"# TYPE qbit_torrent_unmanaged gauge",
	}
	for _, torrent := range items {
		tagged := false
		for _, tag := range strings.Split(str(torrent, "tags"), ",") {
			if strings.TrimSpace(tag) == "ratio" {
				tagged = true
			}
		}
		if str(torrent, "category") == "" && !tagged {
			unmanaged = append(unmanaged, fmt.Sprintf("qbit_torrent_unmanaged{name=%q} 1",
				cut(str(torrent, "name"), nameLimit)))
		}
	}

	return concat(progress, ratio, size, silent, unmanaged), nil
}

// Grabs per indexer over the last 90 days: which ones are earning their place. The label is `name`,
// matching prowlarr_indexer_up, so the two can be joined in PromQL.
func indexerUsage() ([]string, []string) {
	cutoff := time.Now().UTC().AddDate(0, 0, -90)
	counts := map[string]int{}
	var problems []string

	for _, app := range arrs() {
		key, err := apiKey(app.name)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s grabs: %v", app.name, err))
			continue
		}
		for page := 1; page <= 3; page++ {
			records, err := list(fmt.Sprintf("%s/api/%s/history?page=%d&pageSize=200&sortKey=date&sortDirection=descending",
				app.url, app.api, page), key)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s grabs: %v", app.name, err))
				break
			}
			if len(records) == 0 {
				break
			}
			for _, record := range records {
				if str(record, "eventType") != "grabbed" {
					continue
				}
				at, err := when(str(record, "date"))
				if err != nil || at.Before(cutoff) {
					continue
				}
				indexer := str(obj(record, "data"), "indexer")
				if indexer == "" {
					indexer = "?"
				}
				counts[strings.ReplaceAll(indexer, " (Prowlarr)", "")]++
			}
		}
	}

	lines := []string{
		"# HELP arr_indexer_grabs_90d Grabs per indexer in the last 90 days",
		"# TYPE arr_indexer_grabs_90d gauge",
	}
	for _, name := range sorted(counts) {
		lines = append(lines, fmt.Sprintf("arr_indexer_grabs_90d{name=%q} %d", escape(name), counts[name]))
	}
	return lines, problems
}

// Size per title with its quality as a label, which a disk walk cannot give: it only knows paths,
// and guessing quality from a filename is a losing game. Series are "mixed", since a season is many
// files. The library counts ride along here rather than being derived from the size series, which
// would silently answer "titles that happen to have a file".
func librarySizes() ([]string, []string) {
	lines := []string{
		"# HELP arr_media_size_bytes Size on disk per title, with its quality",
		"# TYPE arr_media_size_bytes gauge",
	}
	counts := map[string]int{}
	var problems []string

	if key, err := apiKey("radarr"); err != nil {
		problems = append(problems, fmt.Sprintf("radarr library: %v", err))
	} else if movies, err := list(radarrURL+"/api/v3/movie", key); err != nil {
		problems = append(problems, fmt.Sprintf("radarr library: %v", err))
	} else {
		withFile := 0
		for _, movie := range movies {
			file := obj(movie, "movieFile")
			if !truthy(movie, "hasFile") || len(file) == 0 {
				continue
			}
			withFile++
			name := quality(file)
			if name == "?" {
				name = "Unknown"
			}
			lines = append(lines, fmt.Sprintf("arr_media_size_bytes{app=\"radarr\",title=%q,quality=%q} %.0f",
				cut(str(movie, "title"), nameLimit), escape(name), num(file, "size")))
		}
		counts["radarr/total"] = len(movies)
		counts["radarr/with_file"] = withFile
	}

	if key, err := apiKey("sonarr"); err != nil {
		problems = append(problems, fmt.Sprintf("sonarr library: %v", err))
	} else if shows, err := list(sonarrURL+"/api/v3/series", key); err != nil {
		problems = append(problems, fmt.Sprintf("sonarr library: %v", err))
	} else {
		withFile, episodes := 0, 0
		for _, series := range shows {
			stats := obj(series, "statistics")
			if files := int(num(stats, "episodeFileCount")); files > 0 {
				withFile++
				episodes += files
			}
			size := num(stats, "sizeOnDisk")
			if size == 0 {
				continue
			}
			lines = append(lines, fmt.Sprintf("arr_media_size_bytes{app=\"sonarr\",title=%q,quality=\"mixed\"} %.0f",
				cut(str(series, "title"), nameLimit), size))
		}
		counts["sonarr/total"] = len(shows)
		counts["sonarr/with_file"] = withFile
		counts["sonarr/episodes"] = episodes
	}

	lines = append(lines,
		"# HELP arr_library_titles Titles in each app; kind separates the whole list from what is on disk",
		"# TYPE arr_library_titles gauge")
	for _, key := range sorted(counts) {
		app, kind, _ := strings.Cut(key, "/")
		lines = append(lines, fmt.Sprintf("arr_library_titles{app=%q,kind=%q} %d", app, kind, counts[key]))
	}
	return lines, problems
}

// One series per (film, audio language), so a dashboard can filter by language. The full list
// travels along as the `languages` label on every one of a film's series, so a table can show
// "English, Spanish" on a row matched by language="Spanish" alone: filtering and displaying want
// different shapes and this carries both.
//
// Radarr's own language names, not the per-stream ISO 639-2 codes in mediaInfo: those would need a
// lookup table to be readable and the two agree on every file that carries language tags at all.
func mediaAudio() ([]string, []string) {
	lines := []string{
		"# HELP arr_media_audio 1 per film and audio language present in the file",
		"# TYPE arr_media_audio gauge",
	}
	key, err := apiKey("radarr")
	if err != nil {
		return lines, []string{fmt.Sprintf("radarr audio: %v", err)}
	}
	movies, err := list(radarrURL+"/api/v3/movie", key)
	if err != nil {
		return lines, []string{fmt.Sprintf("radarr audio: %v", err)}
	}

	for _, movie := range movies {
		file := obj(movie, "movieFile")
		if !truthy(movie, "hasFile") || len(file) == 0 {
			continue
		}
		var langs []string
		for _, language := range objs(file, "languages") {
			if name := str(language, "name"); name != "" && !contains(langs, name) {
				langs = append(langs, name)
			}
		}
		sort.Strings(langs)
		if len(langs) == 0 {
			langs = []string{"Unknown"}
		}
		info := obj(file, "mediaInfo")
		codec := str(info, "audioCodec")
		if codec == "" {
			codec = "?"
		}
		channels := str(info, "audioChannels")
		if channels == "" {
			channels = "?"
		}
		name := quality(file)
		if name == "?" {
			name = "Unknown"
		}
		for _, lang := range langs {
			lines = append(lines, fmt.Sprintf(
				"arr_media_audio{title=%q,language=%q,languages=%q,language_count=%q,codec=%q,channels=%q,quality=%q} 1",
				cut(str(movie, "title"), nameLimit), escape(lang), cut(strings.Join(langs, ", "), 120),
				fmt.Sprint(len(langs)), escape(codec), escape(channels), escape(name)))
		}
	}
	return lines, nil
}

// Everything Radarr waits for: downloading (an ETA), missing (no file) or upgrade (below the profile
// cutoff). For the last two there is no date to give, only when the film becomes available in that
// quality at all, which is what whenBetter answers.
func waitingOn() ([]string, []string) {
	lines := []string{
		"# HELP arr_waiting 1 for a title Radarr is waiting on; kind says what it is waiting for",
		"# TYPE arr_waiting gauge",
	}
	key, err := apiKey("radarr")
	if err != nil {
		return lines, []string{fmt.Sprintf("radarr waiting: %v", err)}
	}

	profiles, err := list(radarrURL+"/api/v3/qualityprofile", key)
	if err != nil {
		return lines, []string{fmt.Sprintf("radarr waiting: %v", err)}
	}
	profileName := map[int]string{}
	cutoffName := map[int]string{}
	for _, profile := range profiles {
		id := int(num(profile, "id"))
		profileName[id] = str(profile, "name")
		target := int(num(profile, "cutoff"))
		cutoffName[id] = fmt.Sprint(target)
		for _, item := range objs(profile, "items") {
			itemID := int(num(item, "id"))
			if itemID == 0 {
				itemID = int(num(obj(item, "quality"), "id"))
			}
			if itemID != target {
				continue
			}
			if name := str(item, "name"); name != "" {
				cutoffName[id] = name
			} else {
				cutoffName[id] = str(obj(item, "quality"), "name")
			}
		}
	}

	movies, err := list(radarrURL+"/api/v3/movie", key)
	if err != nil {
		return lines, []string{fmt.Sprintf("radarr waiting: %v", err)}
	}
	today := time.Now().UTC()
	byTitle := map[string]map[string]any{}
	current := map[string]string{}
	expected := map[string]string{}
	for _, movie := range movies {
		title := str(movie, "title")
		byTitle[title] = movie
		if file := obj(movie, "movieFile"); truthy(movie, "hasFile") && len(file) > 0 {
			name := quality(file)
			if name == "?" {
				name = "Unknown"
			}
			current[title] = name
		}
		expected[title] = whenBetter(movie, today)
	}

	type row struct{ kind, current, target, expected string }
	rows := map[string]row{}
	var problems []string

	// downloading wins over the other two: it is already happening.
	if queue, err := list(radarrURL+"/api/v3/queue?pageSize=100&includeMovie=true", key); err != nil {
		problems = append(problems, fmt.Sprintf("radarr waiting: %v", err))
	} else {
		for _, item := range queue {
			title := str(obj(item, "movie"), "title")
			if title == "" {
				title = orQ(str(item, "title"))
			}
			eta := str(item, "timeleft")
			state := str(item, "trackedDownloadState")
			if state == "" {
				state = str(item, "status")
			}
			// An ETA is only honest while bytes are moving: a queue item with nothing downloaded
			// after hours is stalled, and saying "downloading" would hide that.
			answer := state
			switch {
			case eta != "" && eta != "00:00:00" && eta != "0:00:00":
				answer = "eta " + eta
			case num(item, "sizeleft") > 0 && num(item, "sizeleft") == num(item, "size"):
				answer = "stalled, no data yet"
			case answer == "":
				answer = "in queue"
			}
			rows[title] = row{"downloading", withDefault(current[title], "none"), quality(item), answer}
		}
	}

	for _, movie := range movies {
		title := str(movie, "title")
		if _, taken := rows[title]; taken || !truthy(movie, "monitored") || truthy(movie, "hasFile") {
			continue
		}
		rows[title] = row{"missing", "none",
			withDefault(cutoffName[int(num(movie, "qualityProfileId"))], "?"),
			withDefault(expected[title], "unknown")}
	}

	if wanted, err := list(radarrURL+"/api/v3/wanted/cutoff?pageSize=200", key); err != nil {
		problems = append(problems, fmt.Sprintf("radarr waiting: %v", err))
	} else {
		for _, record := range wanted {
			title := str(record, "title")
			if _, taken := rows[title]; taken {
				continue
			}
			rows[title] = row{"upgrade", withDefault(current[title], "none"),
				withDefault(cutoffName[int(num(record, "qualityProfileId"))], "?"),
				withDefault(expected[title], "unknown")}
		}
	}

	titles := make([]string, 0, len(rows))
	for title := range rows {
		titles = append(titles, title)
	}
	sort.Strings(titles)
	for _, title := range titles {
		item := rows[title]
		profile := withDefault(profileName[int(num(byTitle[title], "qualityProfileId"))], "?")
		lines = append(lines, fmt.Sprintf(
			"arr_waiting{app=\"radarr\",title=%q,kind=%q,current=%q,target=%q,expected=%q,profile=%q} 1",
			cut(title, nameLimit), item.kind, escape(item.current), escape(item.target),
			escape(item.expected), escape(profile)))
	}
	return lines, problems
}

// The best honest answer to "when will this improve?". There is no date for "when a good release
// gets uploaded", but there is one for "when the film is available at all": a title still in cinemas
// cannot have a Bluray however often it is searched for, and Radarr carries TMDB's dates.
func whenBetter(movie map[string]any, today time.Time) string {
	for _, field := range []struct{ key, label string }{
		{"digitalRelease", "digital"}, {"physicalRelease", "physical"},
	} {
		value := str(movie, field.key)
		if value == "" {
			continue
		}
		date, err := when(value)
		if err != nil {
			continue
		}
		if date.After(today) {
			return fmt.Sprintf("%s %s", field.label, date.Format("2006-01-02"))
		}
		return "out, waiting for a good release"
	}
	switch str(movie, "status") {
	case "inCinemas":
		if cinema, err := when(str(movie, "inCinemas")); err == nil {
			return fmt.Sprintf("in cinemas since %s, no digital date", cinema.Format("2006-01-02"))
		}
		return "in cinemas, no digital date"
	case "announced":
		return "not released yet"
	}
	return "unknown"
}

// 1 when working, 0 while Prowlarr has it disabled after failures. One series per indexer, so a
// state timeline shows them dropping out and coming back.
func prowlarrIndexers() ([]string, []string) {
	key, err := apiKey("prowlarr")
	if err != nil {
		return nil, []string{fmt.Sprintf("prowlarr: %v", err)}
	}
	indexers, err := list(prowlarrURL+"/api/v1/indexer", key)
	if err != nil {
		return nil, []string{fmt.Sprintf("prowlarr: %v", err)}
	}
	status, err := list(prowlarrURL+"/api/v1/indexerstatus", key)
	if err != nil {
		return nil, []string{fmt.Sprintf("prowlarr: %v", err)}
	}

	now := time.Now().UTC()
	disabled := map[int]bool{}
	for _, entry := range status {
		till := str(entry, "disabledTill")
		if till == "" {
			continue
		}
		if at, err := when(till); err == nil && at.After(now) {
			disabled[int(num(entry, "indexerId"))] = true
		}
	}

	lines := []string{
		"# HELP prowlarr_indexer_up 1 when the indexer is usable, 0 while disabled after failures",
		"# TYPE prowlarr_indexer_up gauge",
	}
	for _, indexer := range indexers {
		up := 1
		if disabled[int(num(indexer, "id"))] {
			up = 0
		}
		lines = append(lines, fmt.Sprintf("prowlarr_indexer_up{name=%q,enabled=%q} %d",
			escape(str(indexer, "name")), str(indexer, "enable"), up))
	}
	lines = append(lines,
		"# HELP prowlarr_indexers_down How many indexers are disabled right now",
		"# TYPE prowlarr_indexers_down gauge",
		fmt.Sprintf("prowlarr_indexers_down %d", len(disabled)))
	return lines, nil
}

// Queries, grabs and failures per indexer, so "is anything being asked of it" is answerable
// separately from prowlarr_indexer_up: an indexer queried 997 times with no grab is a different
// problem from one that is down, and an availability timeline shows them the same.
//
// Counters and not gauges because indexerstats with no date range returns all-time totals:
// increase() gives the rate and a history prune reads as a counter reset. A date range would hand
// Prometheus a pre-averaged number over a window it did not choose.
func prowlarrActivity() ([]string, []string) {
	key, err := apiKey("prowlarr")
	if err != nil {
		return nil, []string{fmt.Sprintf("prowlarr stats: %v", err)}
	}
	var stats map[string]any
	if err := getJSON(prowlarrURL+"/api/v1/indexerstats", key, &stats); err != nil {
		return nil, []string{fmt.Sprintf("prowlarr stats: %v", err)}
	}

	queries := []string{
		"# HELP prowlarr_indexer_queries_total Searches sent to this indexer, all time",
		"# TYPE prowlarr_indexer_queries_total counter",
	}
	grabs := []string{
		"# HELP prowlarr_indexer_grabs_total Grabs taken from this indexer, all time",
		"# TYPE prowlarr_indexer_grabs_total counter",
	}
	failed := []string{
		"# HELP prowlarr_indexer_failed_queries_total Searches this indexer failed, all time",
		"# TYPE prowlarr_indexer_failed_queries_total counter",
	}
	slow := []string{
		"# HELP prowlarr_indexer_response_ms Average response time this indexer answers in",
		"# TYPE prowlarr_indexer_response_ms gauge",
	}
	for _, indexer := range objs(stats, "indexers") {
		label := fmt.Sprintf("{name=%q}", escape(str(indexer, "indexerName")))
		queries = append(queries, fmt.Sprintf("prowlarr_indexer_queries_total%s %.0f", label, num(indexer, "numberOfQueries")))
		grabs = append(grabs, fmt.Sprintf("prowlarr_indexer_grabs_total%s %.0f", label, num(indexer, "numberOfGrabs")))
		failed = append(failed, fmt.Sprintf("prowlarr_indexer_failed_queries_total%s %.0f", label, num(indexer, "numberOfFailedQueries")))
		slow = append(slow, fmt.Sprintf("prowlarr_indexer_response_ms%s %.0f", label, num(indexer, "averageResponseTime")))
	}
	return concat(queries, grabs, failed, slow), nil
}

// Films Maintainerr has queued for deletion: watched, waiting out the grace period. The first stage
// of the retention policy and the only one nothing else can see.
//
// From the paginated media endpoint, not the `media` array on /api/collections, which is capped and
// returned two of three.
func maintainerrPending() ([]string, []string) {
	var collections []map[string]any
	if err := getJSON(maintURL+"/api/collections", "", &collections); err != nil {
		return nil, []string{fmt.Sprintf("maintainerr: %v", err)}
	}

	counts := []string{
		"# HELP maintainerr_pending_media Films queued for deletion, per collection",
		"# TYPE maintainerr_pending_media gauge",
	}
	sizes := []string{
		"# HELP maintainerr_pending_bytes Disk held by films queued for deletion",
		"# TYPE maintainerr_pending_bytes gauge",
	}
	since := []string{
		"# HELP maintainerr_pending_since_timestamp When each film entered the queue",
		"# TYPE maintainerr_pending_since_timestamp gauge",
	}
	perFilm := []string{
		"# HELP maintainerr_pending_film_bytes Disk each queued film would give back",
		"# TYPE maintainerr_pending_film_bytes gauge",
	}
	var problems []string

	for _, collection := range collections {
		title := orQ(str(collection, "title"))
		label := fmt.Sprintf("collection=%q", escape(title))
		var items []map[string]any
		for page := 1; page <= 20; page++ {
			var batch struct {
				Items []map[string]any `json:"items"`
			}
			url := fmt.Sprintf("%s/api/collections/media/%d/content/%d", maintURL, int(num(collection, "id")), page)
			if err := getJSON(url, "", &batch); err != nil {
				problems = append(problems, fmt.Sprintf("maintainerr media page %d: %v", page, err))
				break
			}
			if len(batch.Items) == 0 {
				break
			}
			items = append(items, batch.Items...)
		}

		counts = append(counts, fmt.Sprintf("maintainerr_pending_media{%s} %d", label, len(items)))
		sizes = append(sizes, fmt.Sprintf("maintainerr_pending_bytes{%s} %.0f", label, num(collection, "totalSizeBytes")))
		for _, item := range items {
			name := str(obj(item, "mediaData"), "title")
			if name == "" {
				name = fmt.Sprintf("plex %s", str(item, "mediaServerId"))
			}
			labels := fmt.Sprintf("%s,title=%q", label, cut(name, nameLimit))
			if added := str(item, "addDate"); added != "" {
				if at, err := when(added); err == nil {
					since = append(since, fmt.Sprintf("maintainerr_pending_since_timestamp{%s} %d", labels, at.Unix()))
				}
			}
			perFilm = append(perFilm, fmt.Sprintf("maintainerr_pending_film_bytes{%s} %.0f", labels, num(item, "sizeBytes")))
		}
	}
	return concat(counts, sizes, since, perFilm), problems
}

// The two ways media ends up managed by nothing: a queue item the arr cannot attribute to anything,
// which never imports and is only re-announced when the arr restarts, and data in downloads that no
// torrent claims.
//
// `linked` is a label and not a filter: while the library shares those bytes they cost nothing, and
// the day the retention policy deletes the film that leftover name is the last reference to bytes
// nobody will reclaim.
func orphans() ([]string, []string) {
	type queueRow struct {
		app, title, state, reason string
		size                      float64
	}
	var rows []queueRow
	var problems []string

	queues := []struct{ app, url, idField, unknownArg string }{
		{"radarr", radarrURL, "movieId", "includeUnknownMovieItems"},
		{"sonarr", sonarrURL, "seriesId", "includeUnknownSeriesItems"},
	}
	for _, queue := range queues {
		key, err := apiKey(queue.app)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s queue: %v", queue.app, err))
			continue
		}
		records, err := list(fmt.Sprintf("%s/api/v3/queue?pageSize=200&%s=true", queue.url, queue.unknownArg), key)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s queue: %v", queue.app, err))
			continue
		}
		for _, record := range records {
			state := str(record, "trackedDownloadState")
			stuck := state == "importBlocked" || state == "importFailed"
			if !stuck && num(record, queue.idField) != 0 {
				continue
			}
			reason := ""
			for _, message := range objs(record, "statusMessages") {
				if texts, ok := message["messages"].([]any); ok && len(texts) > 0 {
					reason, _ = texts[0].(string)
				}
				if reason == "" {
					reason = str(message, "title")
				}
				break
			}
			rows = append(rows, queueRow{queue.app, orQ(str(record, "title")), orQ(state), reason, num(record, "size")})
		}
	}

	lines := []string{
		"# HELP arr_orphan_queue_bytes A queue item the arr cannot import without a human",
		"# TYPE arr_orphan_queue_bytes gauge",
	}
	for _, row := range rows {
		lines = append(lines, fmt.Sprintf("arr_orphan_queue_bytes{app=%q,title=%q,state=%q,reason=%q} %.0f",
			row.app, cut(row.title, nameLimit), escape(row.state), cut(row.reason, 70), row.size))
	}
	lines = append(lines,
		"# HELP arr_orphan_queue_items Queue items waiting for a human, per app",
		"# TYPE arr_orphan_queue_items gauge")
	for _, queue := range queues {
		count := 0
		for _, row := range rows {
			if row.app == queue.app {
				count++
			}
		}
		lines = append(lines, fmt.Sprintf("arr_orphan_queue_items{app=%q} %d", queue.app, count))
	}

	type dataRow struct {
		name  string
		size  int64
		links uint64
	}
	var data []dataRow
	downloads := filepath.Join(dataRoot, "downloads")
	if items, err := qbitTorrents(); err != nil {
		problems = append(problems, fmt.Sprintf("qbittorrent: %v", err))
	} else if entries, err := os.ReadDir(downloads); err == nil {
		claimed := map[string]bool{}
		for _, torrent := range items {
			content := str(torrent, "content_path")
			if content == "" {
				continue
			}
			local := strings.Replace(content, "/data/", dataRoot+"/", 1)
			relative, err := filepath.Rel(downloads, local)
			if err != nil || strings.HasPrefix(relative, "..") {
				continue
			}
			claimed[strings.SplitN(relative, string(filepath.Separator), 2)[0]] = true
		}
		for _, entry := range entries {
			// qBittorrent writes downloads in progress into incomplete/.
			if entry.Name() == "incomplete" || claimed[entry.Name()] {
				continue
			}
			size, links := treeBytesAndLinks(filepath.Join(downloads, entry.Name()))
			if size >= minOrphanBytes {
				data = append(data, dataRow{entry.Name(), size, links})
			}
		}
	}

	lines = append(lines,
		"# HELP arr_orphan_data_bytes Data in downloads that no torrent claims any more",
		"# TYPE arr_orphan_data_bytes gauge")
	for _, row := range data {
		shared := "no"
		if row.links >= 2 {
			shared = "yes"
		}
		lines = append(lines, fmt.Sprintf("arr_orphan_data_bytes{name=%q,linked=%q} %d",
			cut(row.name, nameLimit), shared, row.size))
	}
	lines = append(lines,
		"# HELP arr_orphan_data_total_bytes Orphan data, split by whether the library shares it",
		"# TYPE arr_orphan_data_total_bytes gauge")
	for _, shared := range []string{"yes", "no"} {
		var total int64
		for _, row := range data {
			if (row.links >= 2) == (shared == "yes") {
				total += row.size
			}
		}
		lines = append(lines, fmt.Sprintf("arr_orphan_data_total_bytes{linked=%q} %d", shared, total))
	}
	return lines, problems
}

// Total bytes under a path, and the highest link count among its real payload files. Small files are
// ignored for the link count: an nfo or a subtitle is never hardlinked, so counting them would
// report every release as unshared.
func treeBytesAndLinks(path string) (int64, uint64) {
	var total int64
	var links uint64
	filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil //nolint:nilerr // an unreadable entry is not worth failing the whole group
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		if stat, ok := info.Sys().(*syscall.Stat_t); ok && info.Size() > minOrphanBytes/2 {
			if uint64(stat.Nlink) > links {
				links = uint64(stat.Nlink)
			}
		}
		return nil
	})
	return total, links
}

func concat(groups ...[]string) []string {
	var out []string
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

func sorted[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func withDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
