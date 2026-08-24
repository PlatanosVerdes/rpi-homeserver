package main

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Moving the knobs that decide how hard a tracker is worked, from the one number that matters.
//
// The reading gives the headroom; that picks a tier, and the tier sets three things:
//
//	Prowlarr  the indexer's own "Search freeleech only" filter, which every app inherits
//	Radarr    requiredFlags = [1] (freeleech only) or [] (anything)
//	autobrr   how many freeleech grabs a day the ratio builder may take
//
// The filter belongs in Prowlarr and not in the arrs: requiredFlags exists on Radarr's Torznab
// indexer and not on Sonarr's, so filtering there leaves series unprotected, and one 20 GB season
// pack that is not freeleech is the difference between a working account and a disabled one.
//
// Nothing is written when the value is already in place, so this is silent except on the crossings.

const freeleechFlag = 1 // G_Freeleech, in Radarr's indexer flags

// The list fields autobrr's own API rejects a filter for omitting, even when they are empty.
var autobrrLists = []string{
	"resolutions", "sources", "codecs", "containers", "match_hdr", "except_hdr", "match_other",
	"except_other", "origins", "except_origins", "formats", "quality", "media",
	"match_release_types", "match_languages", "except_languages",
}

var arrPorts = map[string]string{"radarr": radarrURL, "sonarr": sonarrURL}

type actor struct {
	changes  []string
	problems []string
}

func (a *actor) changed(format string, args ...any) {
	a.changes = append(a.changes, fmt.Sprintf(format, args...))
}

func (a *actor) failed(format string, args ...any) {
	a.problems = append(a.problems, fmt.Sprintf(format, args...))
}

func tierFor(config map[string]any, headroomGB float64) map[string]any {
	for _, tier := range objs(config, "tiers") {
		ceiling, has := tier["headroom_gb_below"].(float64)
		if !has || headroomGB < ceiling {
			return tier
		}
	}
	return nil
}

func arrIndexer(app, needle string) (string, map[string]any, error) {
	key, err := apiKey(app)
	if err != nil {
		return "", nil, err
	}
	indexers, err := getJSON[[]map[string]any](arrPorts[app]+"/api/v3/indexer", key)
	if err != nil {
		return "", nil, err
	}
	for _, indexer := range indexers {
		if strings.Contains(strings.ToLower(str(indexer, "name")), strings.ToLower(needle)) {
			return key, indexer, nil
		}
	}
	return "", nil, fmt.Errorf("%s has no indexer matching %s", app, needle)
}

// Radarr can be told to take freeleech only, which is the whole point of having the flag.
func (a *actor) setRequiredFlags(app, needle string, freeleechOnly bool) {
	key, indexer, err := arrIndexer(app, needle)
	if err != nil {
		a.failed("%v", err)
		return
	}
	var field map[string]any
	for _, candidate := range objs(indexer, "fields") {
		if str(candidate, "name") == "requiredFlags" {
			field = candidate
		}
	}
	if field == nil {
		a.failed("%s indexer %s has no requiredFlags field", app, str(indexer, "name"))
		return
	}
	want := []any{}
	if freeleechOnly {
		want = []any{float64(freeleechFlag)}
	}
	if sameFlags(field["value"], want) {
		return
	}
	field["value"] = want
	if dryRun {
		a.changed("[dry-run] %s: requiredFlags -> %v", app, want)
		return
	}
	url := fmt.Sprintf("%s/api/v3/indexer/%v?forceSave=true", arrPorts[app], indexer["id"])
	if err := put(url, key, indexer); err != nil {
		a.failed("%v", err)
		return
	}
	a.changed("%s: %s -> %s", app, str(indexer, "name"), pick(freeleechOnly, "freeleech only", "any torrent"))
}

// The one switch every app inherits: the site's FREELEECH facet, applied to every query.
func (a *actor) setProwlarrFreeleech(needle string, only bool) {
	key, err := apiKey("prowlarr")
	if err != nil {
		a.failed("%v", err)
		return
	}
	indexers, err := getJSON[[]map[string]any](prowlarrURL+"/api/v1/indexer", key)
	if err != nil {
		a.failed("%v", err)
		return
	}
	var indexer map[string]any
	for _, candidate := range indexers {
		if strings.Contains(strings.ToLower(str(candidate, "name")), strings.ToLower(needle)) {
			indexer = candidate
			break
		}
	}
	if indexer == nil {
		a.failed("prowlarr has no indexer matching %s", needle)
		return
	}
	var field map[string]any
	for _, candidate := range objs(indexer, "fields") {
		if str(candidate, "name") == "freeleech" {
			field = candidate
		}
	}
	if field == nil {
		a.failed("prowlarr indexer %s has no freeleech filter", str(indexer, "name"))
		return
	}
	current, _ := field["value"].(bool)
	if current == only {
		return
	}
	field["value"] = only
	if dryRun {
		a.changed("[dry-run] prowlarr: freeleech only -> %v", only)
		return
	}
	url := fmt.Sprintf("%s/api/v1/indexer/%v?forceSave=true", prowlarrURL, indexer["id"])
	if err := put(url, key, indexer); err != nil {
		a.failed("%v", err)
		return
	}
	a.changed("prowlarr: %s -> %s", str(indexer, "name"), pick(only, "freeleech results only", "all results"))
}

// Kept to put an indexer back: taking one away was the wrong answer to a filtering problem.
func (a *actor) setIndexerEnabled(app, needle string, enabled bool) {
	key, indexer, err := arrIndexer(app, needle)
	if err != nil {
		a.failed("%v", err)
		return
	}
	switches := []string{"enableRss", "enableAutomaticSearch", "enableInteractiveSearch"}
	same := true
	for _, name := range switches {
		if value, _ := indexer[name].(bool); value != enabled {
			same = false
		}
	}
	if same {
		return
	}
	for _, name := range switches {
		indexer[name] = enabled
	}
	if dryRun {
		a.changed("[dry-run] %s: %s -> %s", app, str(indexer, "name"), pick(enabled, "on", "off"))
		return
	}
	url := fmt.Sprintf("%s/api/v3/indexer/%v?forceSave=true", arrPorts[app], indexer["id"])
	if err := put(url, key, indexer); err != nil {
		a.failed("%v", err)
		return
	}
	a.changed("%s: %s -> %s", app, str(indexer, "name"), pick(enabled, "enabled", "disabled"))
}

// What each tracker's grabber is actually set to, read from autobrr rather than assumed. Only the
// tiers are steered from here; every tracker that names a filter gets its rate reported.
func grabberMetrics(tracker string, config map[string]any) ([]string, error) {
	name := str(config, "autobrr_filter")
	if name == "" {
		return nil, nil
	}
	current, err := autobrrFilter(name)
	if err != nil {
		return nil, err
	}
	perDay := num(current, "max_downloads")
	if strings.EqualFold(str(current, "max_downloads_unit"), "HOUR") {
		perDay *= 24
	}
	label := fmt.Sprintf("tracker=%q", escape(tracker))
	enabled, _ := current["enabled"].(bool)
	return []string{
		fmt.Sprintf("tracker_grabber_enabled{%s} %d", label, boolInt(enabled)),
		fmt.Sprintf("tracker_grabber_per_day{%s} %.0f", label, perDay),
		fmt.Sprintf("tracker_grabber_actions{%s} %.0f", label, num(current, "actions_enabled_count")),
	}, nil
}

// Turn spare disk into free downloads, where the tracker pays for holding data. This ranks the
// tracker's finished torrents by what they earn and tags the best `keep-bonus` up to a disk budget,
// shrinking the budget as free space drops. The tag is separate from the plain `keep` a person adds
// by hand, and every share-limit group excludes both.
func (a *actor) bonusHold(tracker string, config map[string]any, torrents []map[string]any) {
	settings := obj(config, "bonus_hold")
	if len(settings) == 0 {
		return
	}
	hosts := hostSet(config)
	floor := num(settings, "free_gb_floor")
	ceiling := num(settings, "max_hold_gb")
	tag := withDefault(str(settings, "tag"), "keep-bonus")

	var candidates []map[string]any
	for _, torrent := range torrents {
		if hosts[announceHost(torrent)] && num(torrent, "progress") >= 1 {
			candidates = append(candidates, torrent)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return earnedBonus(candidates[i]) > earnedBonus(candidates[j])
	})

	// Below the floor the budget shrinks by exactly what is missing, so the release is proportional
	// rather than all-or-nothing.
	missing := floor - freeGB()
	if missing < 0 {
		missing = 0
	}
	budget := ceiling - missing
	if budget < 0 {
		budget = 0
	}

	wanted := map[string]bool{}
	var total float64
	for _, torrent := range candidates {
		sizeGB := num(torrent, "size") / (1 << 30)
		if total+sizeGB <= budget {
			wanted[str(torrent, "hash")] = true
			total += sizeGB
		}
	}
	var add, drop []string
	for _, torrent := range candidates {
		hash := str(torrent, "hash")
		hasTag := false
		for _, current := range strings.Split(str(torrent, "tags"), ",") {
			if strings.TrimSpace(current) == tag {
				hasTag = true
			}
		}
		switch {
		case wanted[hash] && !hasTag:
			add = append(add, hash)
		case !wanted[hash] && hasTag:
			drop = append(drop, hash)
		}
	}
	if len(add) == 0 && len(drop) == 0 {
		return
	}
	if dryRun {
		a.changed("[dry-run] %s: hold %d torrents (%.0f GB) for the leech bonus (+%d, -%d)",
			tracker, len(wanted), total, len(add), len(drop))
		return
	}
	for _, group := range []struct {
		hashes   []string
		endpoint string
	}{{add, "addTags"}, {drop, "removeTags"}} {
		if len(group.hashes) == 0 {
			continue
		}
		if _, err := qbitPost("torrents/"+group.endpoint, url.Values{
			"hashes": {strings.Join(group.hashes, "|")}, "tags": {tag},
		}); err != nil {
			a.failed("%s: %v", tracker, err)
		}
	}
	a.changed("%s: holding %d torrents (%.0f GB) for the leech bonus, about %.0f%% off every download (+%d, -%d)",
		tracker, len(wanted), total, total/10, len(add), len(drop))
}

func (a *actor) setGrabRate(filterName string, perDay float64, enabled bool) {
	current, err := autobrrFilter(filterName)
	if err != nil {
		a.failed("%v", err)
		return
	}
	nowEnabled, _ := current["enabled"].(bool)
	if num(current, "max_downloads") == perDay &&
		str(current, "max_downloads_unit") == "DAY" && nowEnabled == enabled {
		return
	}
	if dryRun {
		a.changed("[dry-run] autobrr: %s -> %.0f/day", filterName, perDay)
		return
	}
	body, err := autobrrGet[map[string]any](fmt.Sprintf("filters/%v", current["id"]))
	if err != nil {
		a.failed("%v", err)
		return
	}
	body["max_downloads"], body["max_downloads_unit"] = perDay, "DAY"
	body["enabled"] = enabled
	// The update rejects nulls, stores no actions, and needs the indexer link resent.
	var indexers []any
	for _, indexer := range objs(body, "indexers") {
		indexers = append(indexers, map[string]any{"id": indexer["id"]})
	}
	body["indexers"] = indexers
	delete(body, "actions")
	for key, value := range body {
		if value == nil {
			delete(body, key)
		}
	}
	for _, key := range autobrrLists {
		if _, ok := body[key]; !ok {
			body[key] = []any{}
		}
	}
	if err := autobrrPut(fmt.Sprintf("filters/%v", current["id"]), body); err != nil {
		a.failed("%v", err)
		return
	}
	a.changed("autobrr: %s -> %s", filterName,
		pick(enabled, fmt.Sprintf("%.0f grabs/day", perDay), "paused, disk is full"))
	a.checkHasAction(filterName)
}

// A filter with no enabled action matches releases and pushes nothing, and says so only in autobrr's
// own log. Anything that touches the filter checks afterwards.
func (a *actor) checkHasAction(filterName string) {
	listed, err := autobrrFilter(filterName)
	if err != nil {
		a.failed("%v", err)
		return
	}
	if num(listed, "actions_enabled_count") == 0 {
		a.failed("autobrr filter %s has no enabled action: it will match releases and push nothing", filterName)
	}
}

func freeGB() float64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dataRoot, &stat); err != nil {
		return 0
	}
	return float64(stat.Bavail) * float64(stat.Bsize) / (1 << 30)
}

// act moves the knobs from the reading, and reports what it changed and what it could not.
func act(rules map[string]map[string]any, numbers map[string]reading) ([]string, []string) {
	a := &actor{}
	var lines []string
	lines = append(lines,
		"# HELP tracker_grabber_enabled 1 when this tracker's autobrr filter is switched on",
		"# TYPE tracker_grabber_enabled gauge",
		"# HELP tracker_grabber_per_day Grabs a day its filter allows, whatever the unit is set to",
		"# TYPE tracker_grabber_per_day gauge",
		"# HELP tracker_grabber_actions Enabled actions on that filter: zero means it pushes nothing",
		"# TYPE tracker_grabber_actions gauge",
		"# HELP tracker_tier_grabs_per_day Freeleech grabs a day the current tier allows",
		"# TYPE tracker_tier_grabs_per_day gauge",
		"# HELP tracker_grabber_paused_no_disk 1 while the grabber is off for lack of space",
		"# TYPE tracker_grabber_paused_no_disk gauge",
		"# HELP tracker_tier_freeleech_only 1 while the arrs may only take freeleech",
		"# TYPE tracker_tier_freeleech_only gauge")

	torrents, err := qbitTorrents()
	if err != nil {
		a.failed("qbittorrent: %v", err)
	}

	for _, tracker := range sortedKeys(rules) {
		config := rules[tracker]
		// Holding data for a bonus needs no account reading, so it runs for any tracker that asks
		// for it, including the ones with no tiers.
		a.bonusHold(tracker, config, torrents)

		if part, err := grabberMetrics(tracker, config); err != nil {
			a.failed("%s: grabber state: %v", tracker, err)
		} else {
			lines = append(lines, part...)
		}

		// A tracker with no tiers is here for its rules and its hit & run clock, not to be steered:
		// there is nothing to read from it and nothing to switch.
		if len(objs(config, "tiers")) == 0 {
			a.announce(tracker, "")
			continue
		}
		got, ok := numbers[tracker]
		if !ok {
			a.failed("%s: nothing read", tracker)
			continue
		}
		headroomGB := got.Headroom / (1 << 30)
		tier := tierFor(config, headroomGB)
		if tier == nil {
			a.failed("%s: no tier matches %.1f GB", tracker, headroomGB)
			continue
		}
		freeleechOnly, _ := tier["freeleech_only"].(bool)
		perDay := num(tier, "grabs_per_day")
		needle := withDefault(str(config, "prowlarr_indexer"), tracker)

		// A grab cannot be deleted for the length of the hit & run window, so the rate is a disk
		// budget: below the floor the grabber pauses whatever the ratio says.
		space, floor := freeGB(), num(config, "min_free_gb")
		// Hysteresis, or it flaps: space hovers at the floor while torrents are freed, and every
		// crossing would be a write and a Telegram message. A blip talking to autobrr must not take
		// the run down, and assuming the grabber is running only means the floor is applied without
		// the extra 50 GB, which is the safe direction.
		pausedNow := false
		if current, err := autobrrFilter(str(config, "autobrr_filter")); err != nil {
			a.failed("%s: could not read the grabber state: %v", tracker, err)
		} else {
			enabled, _ := current["enabled"].(bool)
			pausedNow = !enabled
		}
		want := floor
		if pausedNow {
			want += 50
		}
		room := space >= want
		if !room {
			a.failed("%s: %.0f GB free, under the %.0f GB floor, grabber paused", tracker, space, floor)
		}

		a.setProwlarrFreeleech(needle, freeleechOnly)
		a.setRequiredFlags("radarr", needle, freeleechOnly)
		// TorrentLeech has series, and Prowlarr is where the filtering happens, so Sonarr keeps the
		// indexer at every tier.
		a.setIndexerEnabled("sonarr", needle, true)
		if perDay > 0 {
			a.setGrabRate(str(config, "autobrr_filter"), perDay, room)
		}
		a.checkHasAction(str(config, "autobrr_filter"))

		label := fmt.Sprintf("tracker=%q", escape(tracker))
		grabs := perDay
		if !room {
			grabs = 0
		}
		lines = append(lines,
			fmt.Sprintf("tracker_tier_grabs_per_day{%s} %.0f", label, grabs),
			fmt.Sprintf("tracker_tier_freeleech_only{%s} %d", label, boolInt(freeleechOnly)),
			fmt.Sprintf("tracker_grabber_paused_no_disk{%s} %d", label, boolInt(!room)))

		a.announce(tracker, fmt.Sprintf(": headroom %.1f GB, ratio %.3f (line %g)",
			headroomGB, got.Ratio, got.MinRatio))
	}

	lines = append(lines,
		"# HELP tracker_control_last_run_timestamp When the knobs were last checked",
		"# TYPE tracker_control_last_run_timestamp gauge",
		fmt.Sprintf("tracker_control_last_run_timestamp %d", time.Now().Unix()))
	return lines, a.problems
}

// announce says what changed, once per tracker, on Telegram and in the log.
func (a *actor) announce(tracker, suffix string) {
	if len(a.changes) == 0 {
		return
	}
	var body strings.Builder
	fmt.Fprintf(&body, "<b>%s</b>%s\n", tracker, suffix)
	for _, line := range a.changes {
		fmt.Fprintf(&body, "- %s\n", line)
	}
	telegram(strings.TrimRight(body.String(), "\n"))
	for _, line := range a.changes {
		logLine(line)
	}
	a.changes = nil
}

func sameFlags(current any, want []any) bool {
	have, _ := current.([]any)
	if len(have) != len(want) {
		return false
	}
	for i := range have {
		if fmt.Sprint(have[i]) != fmt.Sprint(want[i]) {
			return false
		}
	}
	return true
}

func pick(condition bool, yes, no string) string {
	if condition {
		return yes
	}
	return no
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
