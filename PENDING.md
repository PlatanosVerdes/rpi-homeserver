# Pending tasks

## ❌ Bitwarden Secrets Manager — dropped

Tried moving `.env` secrets to [Bitwarden Secrets Manager](https://bitwarden.com/products/secrets-manager/)
but it did not fit the workflow, so it was abandoned. Secrets stay in `.env` (gitignored).

`scripts/bws-run.py` and [docs/secrets-manager.md](docs/secrets-manager.md) are kept only as
a reference in case it is revisited. They are NOT wired into deploy.

---

## Update alert: notify when a service has a newer, non-breaking version (not implemented)

**Goal:** a Grafana alert that fires only when a `versions.env` pin is behind upstream **and**
the versions in between carry no documented breaking change, so it never nags about the ones
that need a manual look.

**2026-08-05/06: all 13 pending bumps (12 safe ones plus Grafana, whose 12->13 jump was read
against its own breaking-changes guide first) were applied by hand, one at a time, each verified
on the Pi before moving to the next.** See `versions.env`'s git history for what changed and why.
The alert itself is still not built — everything below is the design for it.

"Safe" means: read every release note between the pinned tag and upstream latest (not just the
latest one — a multi-version jump can hide a breaking change in the middle) and found no mention
of "breaking". **Caddy is the one exception worth reading before bumping**: v2.11.4's security
patches "may be breaking if your application relies on the buggy behaviors" (Windows-backslash
path normalization, stripHTML, underscore-header handling) — unlikely to matter for this
Caddyfile's plain reverse-proxy blocks, but it is the one case that is not an unconditional yes.

**Grafana 12→13 is a real breaking change**, confirmed on Grafana's own docs, not just a version
number: numeric datasource-id API endpoints disabled by default, `grafana-cli` removed, the Image
Renderer plugin removed, tightened RBAC enforcement, React 18→19. None of these obviously hit this
repo's setup (single org, provisioned dashboards/alerting, no custom RBAC roles), but "obviously"
is not "confirmed", so this one needs a human to actually read the upgrade guide before bumping,
not an alert telling them it is safe.

**Why it is not built yet:** determining "safe" is the hard part and cannot be reduced to a single
API call — this snapshot took multiple registries/APIs to get right:
- `lscr.io` (LinuxServer's registry) authenticates through `ghcr.io`'s token realm, not its own.
- LinuxServer publishes their own current-stable-version API,
  `https://api.linuxserver.io/api/v1/images` (field `version`), which is a better source than
  sorting a raw tag list — LinuxServer images carry many legacy/multi-arch tags that sort wrong
  numerically (e.g. `linuxserver/radarr` has an ancient tag literally named `5.14`, which
  outranks `6.3.0.10514-ls313` under naive version sort). Overseerr specifically is not tracked
  in that API (deprecated from their catalog) and needed its raw tag list instead.
- GHCR's `tags/list` paginates; the default page is not the full list, and recent tags are not
  guaranteed to sort last. `curl ... /tags/list?n=2000` was enough for every repo checked here,
  but do not trust a single unpaginated call in general.
- Everything else (Caddy, Homepage, Jellyfin, Maintainerr, the Prometheus family, Grafana, the
  *arr apps, qBittorrent, FlareSolverr, speedtest-tracker) has real GitHub releases, which is a
  more reliable "latest version" source than any registry AND the only place with real changelog
  text to grep for "breaking" — prefer `gh api repos/<owner>/<repo>/releases` over registry tags
  wherever a GitHub repo exists.
- Three images are pinned by digest specifically because they publish no version tag at all
  (`aceserve`, `qbittorrent-exporter`, `pihole6_exporter`); there is no version number to compare,
  so "is there an update" can only mean "has the `:latest` digest changed", which says nothing
  about whether it is safe.

**Approach when implemented:**
1. A script (bash+jq, matching `sync-arr-config.sh`) that, for each `versions.env` entry with a
   known GitHub repo, walks every release between the pinned tag and latest via
   `gh api repos/<repo>/releases` (or plain `curl` if `gh` is not available on the Pi — check),
   greps each body for "breaking", and pushes
   `service_update_available{name,current,latest,breaking="true|false"}` to Pushgateway.
2. A Grafana alert rule filtering `breaking="false"` only, so `grafana` (or anything else that
   turns up breaking) never fires it — reviewing those stays a manual, deliberate act.
3. Daily cron is plenty; nothing here needs to be fresher than that.
4. `gh` is installed and authenticated on the Pi as `PlatanosVerdes` (confirmed 2026-08-05),
   so use `gh api` rather than token-less `curl` — 5000 req/h authenticated vs. 60/h anonymous,
   comfortable headroom for walking every release of ~17 repos daily.

---

## Grafana v13: file-provisioning a dashboard whose UID previously existed silently no-ops

**Found 2026-08-06** while bringing the manually-imported "Docker monitoring" dashboard under
provisioning. Confirmed with a control test: a brand-new UID provisions fine (creates rows in the
new `resource`/`resource_history` unified-storage tables — the actual source of truth in v13, the
legacy `dashboard` SQL table is no longer written for new creates, only kept for old migrated
rows). But re-provisioning a UID that existed before and was deleted (even after fully removing it
from `dashboard`, `dashboard_tag`, `dashboard_version`, `resource`, and `resource_history`) just
silently does nothing — no error in the logs, the file is read, "finished to provision dashboards"
logs normally, nothing gets created. Disabling the `provisioning` feature toggle
(`GF_FEATURE_TOGGLES_provisioning=false`) did not help either, so there is some other piece of
state (not yet found) still tombstoning that UID.

**Workaround used:** picked a fresh UID (`docker-monitoring`) and even that only saved successfully
through the normal **Import** UI flow, not through file provisioning — a second, related bug: even
a brand-new UID's file got `"failed to save dashboard" ... error="Dashboard not found"` on save.
Gave up chasing the exact cause; not worth more time on a single dashboard.

**Current state:** "Docker monitoring" is NOT provisioned from git — it lives only in Grafana's own
DB, imported by hand with the corrected queries (`container_cpu_usage_seconds_total`,
`container_memory_working_set_bytes`, matching Container Health). Same category as the other
manually-imported community dashboards. If this dashboard is ever lost, re-import it by hand and
fix those same two metric names again; don't bother trying to provision it from a file until this
Grafana bug is understood.

---

## ✅ Plex casting to a TV while at home — SOLVED (2026-08-06, confirmed working)

Casting (Chromecast) and Plex Companion remote control both rely on local mDNS/SSDP discovery,
which does not survive Tailscale routing everything through an exit node — with one active, local
discovery traffic gets swallowed by the tunnel same as any other traffic. Fix: when casting from a
phone that's on the same wifi as the TV, make sure Tailscale has **no exit node selected** (iOS:
Tailscale app → exit node picker at top → "None"). Without an exit node, local traffic bypasses the
tunnel by default and casting works normally. Android has a more granular option too (Tailscale
v1.70+, avatar → App-based split tunneling → exclude the Plex app specifically; Chromecast's own
app is excluded by Tailscale automatically already), useful if an always-on exit node is wanted for
other reasons.

## Cast/control the home TV from outside the house (not implemented)

**Goal:** trigger playback on the home TV from a phone that is away from home (mobile data, another
network), not just watch on the phone itself.

**Why it's not a simple config toggle (but is NOT flatly impossible either):**
- The actual cast protocol, once a target's IP is known, is ordinary routable TCP/UDP (ports
  8008/8009 control, 8443 for Google Home, a wide UDP range for the media stream itself) — nothing
  about the protocol itself requires same-network. This repo's own `docs/tailscale.md` already
  documents standing up the Pi as a **subnet router** (`--advertise-routes=192.168.1.0/24`), which
  would make the home LAN's IPs reachable from a remote tailnet device.
- The actual blocker is **discovery**: Chromecast/Plex Companion find each other via mDNS
  (multicast), and Tailscale is a Layer-3 overlay that does not carry multicast/broadcast traffic
  at all, by design — confirmed on Tailscale's own OSI-model docs, and there are still-open feature
  requests asking for this. A subnet router does not fix that; multicast just doesn't traverse the
  tunnel regardless of routes.
- Bridging mDNS across that gap for real is possible but genuinely fragile: converting multicast
  announcements into targeted unicast for a specific tailnet peer (custom reflector trickery), or
  running VXLAN over Tailscale on Linux to fake a real L2 segment. Neither is a quick win, and
  consumer apps (Google Home, Plex mobile) don't offer "enter the Chromecast's IP manually" as a
  fallback, so even a working subnet route doesn't help without solving discovery first.

**Approach when implemented:** given the above, the practical path is still to sidestep the mDNS
problem entirely rather than solve it: a local hub that already sits on the LAN (so it does its own
normal local discovery) and that you command *remotely* over Tailscale as an ordinary HTTPS call —
i.e. Home Assistant running on the Pi, using its Google Cast integration
(`media_player.play_media` service call to a cast entity), exposed to the tailnet the same way
every other `*.platanosverdes.com` service is. Not started: no Home Assistant instance exists in
this repo yet — would need its own compose service, Caddy route, and a first pass at the Cast
integration to see how reliably it discovers cast targets before promising this actually works
end to end.

---

## Acestream channel health metric (not implemented)

**Goal:** emit `acestream_channel_health{channel,group} = 1|0` from the `acestream-updater` Go
service, so a channel-health dashboard is possible again (the old "Live TV" dashboard was deleted
because this metric never existed).

**Why it is not done yet:** a previous naive approach saturated the Pi. The `healthURL` in
`services/acestream-updater/main.go` (`aceserveURL + hash`) is the actual acestream **stream** URL,
so probing it makes the P2P engine *start downloading* that channel. Doing that for all channels,
concurrently and/or frequently, melts the Pi's CPU and network. Acestream health is inherently
heavier than an HTTP HEAD because the engine must resolve the stream, so it must be done carefully.

**Approach when implemented** (inside `acestream-updater`, which is already a long-lived container
that pushes metrics — do NOT use an external cron, overlapping runs compound the load):

1. A separate `time.Ticker` goroutine on a slow cadence (every 15-30 min).
2. Bounded concurrency: worker pool of 2-3 (`sem := make(chan struct{}, 3)`), never all channels at once.
3. Short per-channel timeout (`context.WithTimeout`, ~6s); on first 200/first bytes set health=1 and
   `cancel()` immediately, then call acestream's stop/command URL to free the engine session. Never
   read the full stream.
4. Rolling subset per sweep (e.g. 10 channels/cycle) so all channels are covered over ~1h without spikes.
5. Push to a SEPARATE pushgateway job (`.../job/acestream_health`): `pushMetrics` uses PUT to
   `.../job/acestream_updater`, so mixing cadences would clobber it.

---

## Podman instead of Docker — considered, not worth it

**Verdict: no.** The migration touches most of the repo and the thing it buys is one this server
does not need.

### What it would touch

| Piece | Why it is not a drop-in |
| :--- | :--- |
| `docker-compose.yml` | The entry point is `include:` across four modules. `podman-compose` support for `include` is the first thing that would have to be proven, and the whole layout rests on it |
| Homepage, cAdvisor, Vector | All three talk to `/var/run/docker.sock`. Podman's socket is Docker-API-compatible and lives elsewhere; Homepage and Vector would probably follow, cAdvisor is the doubtful one |
| `restart: unless-stopped`, 30+ services | There is no daemon to restart anything. Podman does it through systemd units (quadlet) or `podman-restart.service`, so the semantics have to be rebuilt rather than translated |
| `/etc/docker/daemon.json` | Ignored. Podman logs to journald, so the `10m x 3` cap described in SYSTEM_NOTES stops existing and the journal cap becomes the only thing standing between the logs and the SD card |
| PUID/PGID on every linuxserver image | Rootless Podman maps UIDs into a subordinate range, which collides with the ownership of files on the external disk. Running Podman rootful avoids it and throws away the main reason to switch |
| `network_mode: host` on 5 services, Caddy on 80/443 | Rootless cannot bind below 1024 without extra configuration |
| 8 distinct `docker` subcommands across `scripts/`, plus `apply.sh` | Mostly an alias away, mostly |

### What it would buy

No root daemon, if run rootless. That is the real argument for Podman and it matters on a machine
exposed to the internet. This one is not: there is no port forwarding, the only inbound path is the
Cloudflare tunnel to the deploy webhook, and 30 days of logs show zero unsolicited connection
attempts. The gain is close to theoretical here, and running rootful to dodge the UID problem would
remove even that.

### When to revisit

If the Pi ever gets a service genuinely exposed to the internet, or if Docker's licensing or
upstream direction becomes a problem. As a way to learn Podman it is a fine project; as an
improvement to this server it is not.

---

## Hardware transcoding on the Pi (v4l2m2m) — tried, reverted

Jellyfin transcodes with `libx264` because `EnableHardwareEncoding` is off, and a Pi 4 cannot
software-encode 1080p sport in real time: measured at **1.39x on 1080p25 synthetic video with the
machine near idle**, which halves on a 50fps stream, and the player then restarts every few
seconds. This is the "the stream keeps cutting" symptom, and it only affects clients that cannot
direct play. A TV app playing the same channel, even at 4K, copies bytes and is unaffected.

Mapping `/dev/video10-12` into the container and turning the setting on did not work:

```
[h264_v4l2m2m] VIDIOC_STREAMON failed on output context
[vost#0:0/h264_v4l2m2m] Error encoding a frame: No such process
Conversion failed!
```

The encoder itself is fine in isolation, `h264_v4l2m2m` encodes synthetic 1080p at **2.45x against
libx264's 1.39x**, so the fault is in the real transcode rather than the hardware. Untested
suspects, in the order worth trying: `gpu_mem` too low for the codec firmware, several ffmpeg
processes contending for the single encoder at `/dev/video11` (the player retried four times in
ninety seconds, and each retry starts another), and the `high` profile at `level 42` the transcode
asks for.

**The better fix is upstream of all this: stop transcoding.** The source is H.264, which browsers
play natively, so the work is finding why Jellyfin will not direct play or remux it. Transcoding
efficiently is a worse answer than not transcoding.

---

## The retention policy does not cover series (not implemented)

Films have the full cycle: Maintainerr deletes the library copy two days after you watch one, and
`scripts/seed-cleanup.py` drops the torrent once its tracker is paid. Series have neither half.

Maintainerr's only collection is `type: movie` on library `1` (Películas), so **nothing ever deletes
a watched episode**. And the cleanup only ever acts once the library has let go of a file, which for
an episode never happens, so those torrents stay hardlinked forever. `series/` grows without a
ceiling: 144 GB today against 972 GB of films, slow rather than harmless.

**What it needs:** a second Maintainerr collection over the Series library, same rule shape ("last
viewed more than N days ago"), `arrAction=1` (`UNMONITOR_DELETE_ALL`) and `sonarrSettingsId` pointing
at the Sonarr instance — the movie collection had `radarrSettingsId` unset and silently deleted
through Plex instead, which is what caused the Cars 3 re-download loop (see SYSTEM_NOTES.md). The
cleanup script needs no changes: it already asks Sonarr as well as Radarr what a download produced.

**The decision that is actually open** is `N`, and it is not the same question as for films. A series
watched week to week is "finished" only after the last episode, and deleting episode 3 while you are
on episode 5 loses the option of rewatching a season. Two days, the film threshold, is clearly wrong
here. Worth considering instead: keep whole seasons until the season is fully watched, which
Maintainerr can express, and give it a longer threshold than a film gets.

---

## Private trackers: know the rules, encode them, and stop maintaining bespoke code

Opened on 2026-08-20, the day a TorrentLeech account was disabled and reinstated with **14 days to
fix the ratio (until 2026-09-03)**, and revised on 2026-08-21 once the ratio was over the line
again. Background in [docs/private-trackers.md](docs/private-trackers.md) and
[docs/seeding-and-ratio.md](docs/seeding-and-ratio.md).

### 1. Learn how each private tracker actually works

TorrentLeech is documented, because staff spelled it out: minimum ratio 0.4, three uncleared hit &
runs, one download slot, freeleech excluded from the ratio denominator, 240 h or ratio 1:1 to clear
a hit & run, and a passkey reset on every disable. DigitalCore is half known (freeleech periods,
upload multipliers, bonus points, the per-torrent `Connectable` column). **C411, BTSCHOOL and
retrotoon.world are unknown**, and being unknown is exactly how the last account was lost.

Per site, the six answers that matter: minimum ratio, what triggers a hit & run and how it clears,
any minimum seed time per torrent, how many download slots the current class allows, whether
freeleech exists and how it is flagged, and what the bonus-point shop sells.

### 2. Put the right setup on each tracker

The rules from (1) belong in configuration, not in anyone's memory. Where things stand:

| Tracker | Grab rule | Seed goal |
| :--- | :--- | :--- |
| TorrentLeech | **done, and automated**: the tier in `config/trackers/rules.json` drives Prowlarr's own freeleech filter, which every app inherits, plus `requiredFlags` in Radarr as a backstop | **done**: 360 h / ratio 1.2 in `seed-rules.json` |
| DigitalCore | open: its Cardigann definition carries its own `freeleech` toggle | open, on the generic 240 h / 1.0 |
| C411 | open | open |
| BTSCHOOL | open | open |
| retrotoon.world (Generic Torznab) | open | open |

**The freeleech-only flag was never a decision, it was a control loop, and it is one now.**
`tracker-control.py` moves it, the Sonarr indexer and the autobrr grab rate from a single number, the
headroom (`buffer / min_ratio`, the GB of non-freeleech downloads that still fit above the line).
Tiers live in `config/trackers/rules.json`; see the section in
[docs/private-trackers.md](docs/private-trackers.md).

Two things that turned up while building it and are worth not forgetting:

- **Sonarr has no `requiredFlags` field**, so the "freeleech only, in Radarr and Sonarr" line above
  was never true in Sonarr. Taking the indexer away from Sonarr was the first fix and it was the
  wrong one: TorrentLeech has series, and Prowlarr full-syncs indexers back to the *arrs anyway. The
  filter belongs in Prowlarr, whose Cardigann definition carries a `Search freeleech only` checkbox
  that every app inherits.
- **The grab rate is a disk budget, not a preference**, because a grab cannot be deleted until its
  hit & run window closes. The loop therefore also honours a free-space floor, which overrides the
  ratio in both directions.

What is still open here is only the other four sites: each needs its own entry in
`config/trackers/rules.json`, which needs (1) first, and needs checking whether its profile page can
be read the same way.

Still open per tracker: what each one actually demands, before its seed goal can be set honestly
rather than guessed.

**The trap to remember when editing any of this:** `sync-arr-config.sh` reports success and applies
nothing when Radarr holds a custom format the repo does not list. Measured on 2026-08-21 with 13
formats live against 12 in the repo: the profile PUT returned 202 and changed neither the scores nor
`cutoffFormatScore`. Deleting the stray format made the same sync work. Worth hardening so the
script sends every format Radarr knows (score 0 for the unlisted) and reports failures instead of
counting processed profiles.

### 3. Alerts and a Grafana row for the tracker numbers

**Done for TorrentLeech on 2026-08-21**, by `scripts/tracker-stats.py`: `tracker_ratio`,
`tracker_buffer_bytes`, `tracker_headroom_bytes`, `tracker_points`, `tracker_warning_seconds`,
`tracker_hnr_pending`, `tracker_hnr_at_risk` and a per-torrent `tracker_hnr_torrent_hours_left`,
with four alerts on top (headroom under 10 GB, ratio below the line, an obligation whose clock is
stopped, and no reading in two hours).

**The credential blocker turned out not to be one.** There is still no API, but the site logs in from
a plain form POST and Prowlarr already holds the username and password for the same site, so nothing
new had to be stored: read the credentials from Prowlarr's own API, keep the session cookie on disk,
and re-login only when it stops working. DigitalCore's API key still returns 403 on every user
endpoint (`/api/v1/user`, `/users/current`, `/account`, `/user/stats`, `/me`), which is why this is
per-site work rather than one generic exporter.

What is left: a Grafana row for these series (the alerts exist, the panels do not), and the same
treatment for the other four sites, each of which needs its login form and profile shape checked
once. The parsing in `tracker-stats.py` is deliberately dumb about markup (strip the tags, read the
value on the line after its label) precisely so a second site is a config entry rather than a
selector hunt.

### 4. Adopt the standard tools and retire the bespoke script

Decided on 2026-08-21, and it reverses an earlier position: keeping `seed-cleanup.py` was defended
on the grounds that it works and is understood, but the day produced two silent failures in
home-made scripts (the arr-config sync above, and the client-versus-tracker clock gap that was three
hours from deleting a torrent that still owed 88 hours). Bespoke code **is** the mess when nobody
wants to maintain it. So, in this order:

1. **cross-seed first.** Free ratio and not one extra byte: it finds the same content on other
   trackers and seeds the same files there via hardlinks. Talks to Prowlarr (one Torznab URL per
   indexer, with the API key) and injects directly into qBittorrent. Nothing here has to change to
   make room for it, and `seed-cleanup.py` already refuses to touch a path shared by more than one
   torrent, so cross-seeds are protected from day one.
2. **qbit_manage with `--dry-run`, in parallel** with the current script, comparing decisions until
   they agree. It covers everything `seed-cleanup.py` does (per-tracker share limits, the
   `nohardlinks` rule, orphaned data) plus two things it lacks: **removing unregistered torrents**,
   which would have caught the reset passkey on the first pass instead of four days later, and
   explicit cross-seed protection.
3. **Retire `seed-cleanup.py`** once a week of dry-run agrees. Three rules must survive the move:
   the per-tracker goals (360 h on TorrentLeech, because its clock runs behind the client's), the
   public-tracker rule of dropping a torrent as soon as the library has the file, and the absolute
   one, that nothing is deleted while any tracker is still owed.
4. **autobrr**, planned last but **done first, on 2026-08-21**, because the ratio was the thing
   under a deadline and this is what fixes it: it reacts to a tracker's IRC announce in seconds
   rather than the minutes an RSS poll costs, which is the difference between being an early seeder
   and arriving to a swarm that already has thirty. Concurrency is capped at one download by the
   client rule, not by the filter. Full configuration and the two API traps are in
   `docs/private-trackers.md`. What is left is one filter per remaining tracker, which waits on
   knowing each one's rules.

The one thing `seed-cleanup.py` does that qbit_manage cannot is ask Radarr and Sonarr what a
download actually produced, which mattered for RAR releases. That reason disappeared on 2026-08-21
when RAR packs became a hard reject.

And Jackett is deliberately **not** part of this: it is Prowlarr's predecessor, Prowlarr is already
here, and both cross-seed and autobrr integrate with Prowlarr directly.
