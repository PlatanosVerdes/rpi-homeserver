# Pending tasks

## ❌ Bitwarden Secrets Manager — dropped

Tried moving `.env` secrets to [Bitwarden Secrets Manager](https://bitwarden.com/products/secrets-manager/)
but it did not fit the workflow, so it was abandoned. Secrets stay in `.env` (gitignored).

The wrapper script and its setup guide were deleted on 2026-08-22: they were wired into
nothing and read as live documentation. Recover them from git history if this is revisited.

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
1. A script (bash+jq, matching `scripts/sync/arr-config.sh`) that, for each `versions.env` entry with a
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
  about the protocol itself requires same-network. The Pi is already a **subnet router**, and
  widening what it advertises from its own two `/32`s to `192.168.1.0/24` would make the home LAN's
  IPs reachable from a remote tailnet device (see `docs/tailscale.md` for why it does not do that
  today).
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
| 8 distinct `docker` subcommands across `scripts/`, plus `scripts/deploy/apply.sh` | Mostly an alias away, mostly |

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
`scripts/trackers/seed-cleanup.py` drops the torrent once its tracker is paid. Series have neither half.

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

### 1. Learn how each private tracker actually works, one at a time

The order agreed on 2026-08-21: **one site per sitting**, and each one leaves the guide in
[docs/private-trackers.md](docs/private-trackers.md) a little longer. TorrentLeech is the worked
example, and its section is the shape the others should end up with: rules first, then the
configuration applied here, then why, then its links. The checklist for a new one is at the end of
that page.

Two things the C411 diagnosis added to the method, both worth doing before assuming the worst:

- **A failing indexer is not necessarily a disabled account.** Ask Prowlarr what it actually saw:
  `POST /api/v1/indexer/test` with the indexer's own body returns the real error, and
  `GET /api/v1/health` says how long it has been failing. C411's answer was a broken SOCKS proxy.
- **Check whether the site is reachable at all before touching credentials**, because a dead proxy,
  a dead definition (Frozen Layer has no definition file at all) and a dead account look identical
  from the outside.



**Read on 2026-08-21: DigitalCore, retrotoon.world, BTSCHOOL and C411**, all four written up in
[docs/private-trackers.md](docs/private-trackers.md) with their rules, what is configured for them
and why. Their rules of record are in `config/trackers/rules.json`, and `scripts/trackers/stats.py` now
measures the hit & run clock for every tracker in that file, not only the one it can log into.

What each one still owes:

| Tracker | Known | Missing |
| :--- | :--- | :--- |
| TorrentLeech | everything, from staff | nothing |
| DigitalCore | **everything**, rules and FAQ both: ratio 0.5, H&R at 5 days or 1:1 from 10% downloaded, leech bonus, banned clients, 90-day inactivity | nothing about the rules. An autobrr filter is available, since they support it by name |
| retrotoon.world | 72 h per torrent within 10 days, no ratio rule, site-wide freeleech for 40 days | its **announce host**, unknown until the first torrent arrives |
| C411 | ratio 0.8 to leech, 50 GB signup credit, H&R disabled for now (72 h when it returns), cross-seeding allowed | a way to read the account. Its definition authenticates by API key, so there is no login for `stats.py` to reuse, and it exposes no freeleech facet either |

### DigitalCore's leech bonus is the cheapest ratio on this box

Their FAQ, read 2026-08-21: **10 GB actively seeded buys 1% off what every download costs against
ratio**, averaged over seven days, and **1 TB seeded is 100%, a site-wide freeleech**. The account
is at 12% with about 272 GB seeded.

Two rules shape the play, and both point away from what TorrentLeech rewards:

- **only 50 GiB per torrent counts**, so many medium torrents beat a few enormous ones;
- the bonus is scaled by `1 + (1 / seeders)`, so **being the only seeder of something scarce pays
  double**.

Add their automatic freeleech on anything 15 GB or larger and every new torrent's first 24 hours, and
the cheapest path to unlimited downloading on this box runs through DigitalCore rather than
TorrentLeech.

**Tried and rejected the same day.** Holding that tracker's torrents to grow the bonus was
implemented, measured and turned off: of a 27% bonus, 9% comes free from torrents that share inodes
with the library and 18% was bought with 179 GB of separate copies, on an account already at ratio
4.21 with 86 GB of headroom. That disk is worth more to the TorrentLeech grabber. The bonus grows
through hardlinks only, which is what cross-seed produces anyway. The mechanism stays in
`scripts/trackers/control.py`, unconfigured, for the day the arithmetic reverses.

**Done on 2026-08-21**: autobrr now watches their `#announce` channel with the nick
`PlatanosVerdes` and bot mode on, filtering freeleech between 15 and 30 GB at one a day. The passkey
and IRC key came from the site's own Settings page. Details and the four API traps are in
[docs/private-trackers.md](docs/private-trackers.md).

autobrr also ships a `retrotoonworld` definition and its passkey is already known from the announce
URL, so that one is a decision rather than a blocker: the site is cartoons only, and its 40-day
site-wide freeleech is the reason to bother.

### C411 has 2 GB of headroom and nobody was watching it

The site shows ratio **0.83** against a minimum of **0.8** to download anything, which is
`(52.2 - 0.8 x 63.3) / 0.8 = 1.95 GB` of paid downloads left. One ordinary film blocks leeching
there.

Worse, the ratio is propped up by a **50 GB signup upload credit** counted into it: only 2.3 GB were
really uploaded. The credit is a one-off and does not grow back.

Nothing automatic can help until the account can be read, and the reason is not a forgotten
password: the site's definition authenticates with an **API key**, so there is no username and
password field on its Prowlarr entry, and `scripts/trackers/stats.py` reads a site by logging into it. So
what is left to decide is whether to store a real login for the site alongside the API key, which is
a question about where credentials live rather than a code change. Search itself is fixed, see below.
Until the account can be read the rule is manual: **on C411, freeleech only.**

### BTSCHOOL failed its probation, and the account was let go

The assessment ran out on 2026-08-24 at 09:40 with zero of the 50 GB uploaded, 50 GB downloaded and
6000 bonus points it asked for, which was the expected outcome: 50 GB of upload in three days is not
something seeding produces on demand, and the only other way through was a donation. The indexer and
every trace of it in configuration are gone, and the reasoning is kept in
[docs/private-trackers.md](docs/private-trackers.md) so the site is not tried again the same way.

Per site, the six answers that matter: minimum ratio, what triggers a hit & run and how it clears,
any minimum seed time per torrent, how many download slots the current class allows, whether
freeleech exists and how it is flagged, and what the bonus-point shop sells.

**C411's search is fixed, it was never a ban, and it searches direct.** The `nordvpn` tag came off on
2026-08-21 because the Socks5 credentials at `nl.socks.nordhold.net:1080` had expired, which is what
`Failed to authenticate with the SOCKS server` meant, and it stays off. Those credentials do
authenticate again, which is not a reason to go back: a proxy whose service credentials lapse in
silence produces a failure indistinguishable from a banned account, and it already cost one
diagnosis. Direct, a search returns about a hundred results.

The accepted cost is the ISP's match-night interception, which is what took the indexer down on
2026-08-22. It is now an outage of the length of the window rather than of a day, because
`scripts/ops/indexer-retry.py` clears Prowlarr's backoff within 15 minutes of the site answering
again.

The `Socks5` proxy entry itself is still configured in Prowlarr with no indexer tagged to it. Worth
deleting the next time that screen is open, or it is a trap for whoever tags something with
`nordvpn` next.

Still open on this site: a way to read the account, which needs a stored site login rather than the
API key the definition uses, and the freeleech-only filter, which its definition does not expose at
all.

**Frozen Layer is gone, on 2026-08-24.** It reported `Indexers have no definition and will not work`
because its definition had been deleted upstream: nothing named `frozen` is left in
`Prowlarr/Indexers`, and what kept it searching was a local copy in `/config/Definitions` last
touched on 1 April, pinning a certificate fingerprint that expired in February.

What decided it was not the definition but the noise: Prowlarr's Telegram connection has
`onHealthIssue` on, this was error level rather than a warning, and Prowlarr restarts on every
deploy that rebuilds it. So it sent a message about an unfixable thing on a schedule, and an alert
you learn to ignore costs more than a public indexer that Nyaa.si already covers. Health is clean
now, no warnings at all. The definition file is still on disk if it is ever wanted back.

### Maintainerr's rule and autobrr's filters are in git now, one way only

Both were in `appdata` alone, so the nightly archive was the only copy of them: three autobrr
filters that decide what gets grabbed, and the Maintainerr rule that deletes a film two days after
it is watched. `scripts/ops/config-export.py` pulls both into `config/`, and a deploy runs it with
`--check` and logs `[config-drift]` when the live config and the committed copy differ.

It does not push, and that is deliberate rather than unfinished. `scripts/sync/arr-config.sh` can
push because a wrong quality profile downloads the wrong file; a wrong Maintainerr rule deletes the
library. If pushing is ever wanted, autobrr is the safe half to start with, since its API takes a
whole filter on `PUT /api/filters/{id}` and the worst case is a grab nobody asked for.

Still `appdata` only, with no way around it: Prowlarr, because every indexer it holds carries a
passkey or an API key and this repo is public.

### 2. Put the right setup on each tracker

The rules from (1) belong in configuration, not in anyone's memory. Where things stand:

| Tracker | Grab rule | Seed goal |
| :--- | :--- | :--- |
| TorrentLeech | **done, and automated**: the tier in `config/trackers/rules.json` drives Prowlarr's own freeleech filter, which every app inherits, plus `requiredFlags` in Radarr as a backstop | **done**: 360 h / ratio 1.2 in `seed-rules.json` |
| DigitalCore | open: its Cardigann definition carries its own `freeleech` toggle | open, on the generic 240 h / 1.0 |
| C411 | open | open |
| retrotoon.world (Generic Torznab) | open | open |

**The freeleech-only flag was never a decision, it was a control loop, and it is one now.**
`scripts/trackers/control.py` moves it, the Sonarr indexer and the autobrr grab rate from a single number, the
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

**The trap to remember when editing any of this:** `scripts/sync/arr-config.sh` reports success and applies
nothing when Radarr holds a custom format the repo does not list. Measured on 2026-08-21 with 13
formats live against 12 in the repo: the profile PUT returned 202 and changed neither the scores nor
`cutoffFormatScore`. Deleting the stray format made the same sync work. Worth hardening so the
script sends every format Radarr knows (score 0 for the unlisted) and reports failures instead of
counting processed profiles.

### 3. Alerts and a Grafana row for the tracker numbers

**Done for TorrentLeech on 2026-08-21**, by `scripts/trackers/stats.py`: `tracker_ratio`,
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
once. The parsing in `scripts/trackers/stats.py` is deliberately dumb about markup (strip the tags, read the
value on the line after its label) precisely so a second site is a config entry rather than a
selector hunt.

### 4. Adopt the standard tools and retire the bespoke script

Decided on 2026-08-21, and it reverses an earlier position: keeping `scripts/trackers/seed-cleanup.py` was defended
on the grounds that it works and is understood, but the day produced two silent failures in
home-made scripts (the arr-config sync above, and the client-versus-tracker clock gap that was three
hours from deleting a torrent that still owed 88 hours). Bespoke code **is** the mess when nobody
wants to maintain it. So, in this order:

1. **cross-seed first.** Free ratio and not one extra byte: it finds the same content on other
   trackers and seeds the same files there via hardlinks. Talks to Prowlarr (one Torznab URL per
   indexer, with the API key) and injects directly into qBittorrent. Nothing here has to change to
   make room for it, and `scripts/trackers/seed-cleanup.py` already refuses to touch a path shared by more than one
   torrent, so cross-seeds are protected from day one.
2. **qbit_manage, in evaluation since 2026-08-21.** Running hourly and writing **tags only**:
   `tag_update`, `tag_tracker_error`, `tag_nohardlinks`. Everything that deletes is off
   (`rem_unregistered`, `share_limits`, `rem_orphaned`, `cat_update`). Config in
   `appdata/qbit-manage/config.yml`, which the tool rewrites itself, so it is not in git.

   **Approved on 2026-08-21, and done on 2026-08-24: qbit_manage owns deletion now.** The plan was to
   compare for a week, then enable `rem_unregistered`, then `share_limits`, then park the script, one
   step a week into September. It went in one step instead, for two reasons that only turned up when
   the numbers were looked at:

   - **The deletion is reversible.** `share_limits` cleanup routes through `tor_delete_recycle`, so
     what it removes lands in `/data/downloads/.RecycleBin` and stays there for 7 days
     (`empty_after_x_days`). A wrong group costs a restore, not a re-download.
   - **Nothing was at risk on the first pass.** Computed against the live client before flipping the
     flag: zero torrents past their limits, 23 with no group at all because the library still shares
     their bytes, 15 active in the last day and 7 under their limits. The switch freed 0 GB on day
     one and every group's limit sits above its site's obligation (360 h against TorrentLeech's 240,
     240 against the 120, 96 and 72 of the rest), so no torrent can be deleted while still owing.

   What changed, all of it revertible by uncommenting one cron line:

   1. `share_limits: true` in commands, `cleanup: true` in all four groups.
   2. `scripts/trackers/seed-cleanup.py` commented out of `scripts/crontab`, left in place rather
      than deleted.
   3. Its two alert rules, `homelab-seed-cleanup-failed` and `homelab-seed-cleanup-stale`, paused
      with `isPaused: true`. The stale one fires after three hours, so parking the script without
      this means an alert the same evening saying exactly what was intended.

   **What is still owed, and this is the part to come back to:**

   - **The reporting went dark and nothing replaced it.** The script fed 18 panels across the
     Retention and Disk Usage dashboards, and `DRY_RUN=1` deliberately does not push metrics, so
     there is no way to keep them alive while it is parked. The panels that protect the accounts
     (`tracker_hnr_*`, obligations and stopped clocks) come from `scripts/trackers/stats.py` and are
     unaffected.
   - **`rem_unregistered` is still off.** It is the feature the tool was adopted for and the safest
     deletion there is: it only removes what the tracker itself has disowned. Turn it on next.
   - **Deletion is now slower than it was**, which is the opposite of the usual worry. The groups
     mirror the generic 240 h rather than each site's own number, so `The.Furious.2025` on
     DigitalCore at 168 h would have gone today under the script and now waits until 240 h. If disk
     matters more than margin, DigitalCore wants its own group at its real 5 days plus margin.
   - **Anything added by hand is invisible.** No category means it can never be tagged `noHL`, and
     without autobrr it has no `ratio` tag either, so no group matches it and nothing will ever
     reclaim it. `Obsession (2026)`, added 2026-08-09, was exactly that and was tagged `ratio` by
     hand on 2026-08-24. A check for torrents carrying no `~share_limit_*` tag after a run would
     catch the next one.
   - **The three cross-seeds stay forever.** `cross-seed-link` is not in `nohardlinks`, correctly,
     since a cross-seed shares its bytes by definition. It does mean the second torrent outlives the
     film, which is the trade already recorded below.
   - **The docs still describe the script as the thing that deletes**, in `README.md`,
     `docs/private-trackers.md`, `docs/architecture.md` and `docs/seeding-and-ratio.md`. They are
     left alone on purpose while this is reversible. When the script is deleted for good, so are
     its two alert rules, its 18 panels and those paragraphs.

   One thing to fix while evaluating: the `nohardlinks` list names `tv-sonarr`, and qbit_manage
   reports no torrents in that category, so series are currently outside the check.

   **Measured on 2026-08-24, and the three gaps found are closed in `config/qbit-manage/config.yml`.**
   All three were config, no code:

   1. **20 of the 46 torrents could never be tagged `noHL`**, because `tag_nohardlinks` only inspects
      the categories in the `nohardlinks` list and 17 carry no category at all. Those 17 are
      autobrr's ratio grabs, every one on a private tracker, 538.7 GB that
      `scripts/trackers/seed-cleanup.py` was the only thing judging. The fix is not a category: autobrr
      already tags everything it grabs `ratio`, so the groups now ask for
      `include_any_tags: [noHL, ratio]`, which reads as "the library let go of it, or it was never in
      the library". One torrent from 2026-08-09 predates autobrr and carries neither tag, so it stays
      outside until it is tagged by hand.
   2. **retrotoon fell into the `private` group, which clears at ratio 1.0**, on a site whose rules
      page states no ratio rule at all. It now has its own group at priority 2 with `max_ratio: -1`,
      so only its 72 h of seeding clears anything there.
   3. **"Still paying" had no equivalent**, so a freeleech grab still uploading would have been
      deleted on time alone, which is the one thing that must not happen to a torrent whose whole
      purpose is ratio. Every private group now carries `min_last_active: 1d`. It is coarser than the
      0.2 GB/day floor the script uses, since any activity resets it.

   Verified with the tool itself, on a copy of the config with `share_limits: true`: the groups pick
   up 14 torrentleech, 2 retrotoon, 6 private and 0 public, and `min_last_active` holds most of them
   back with `Min inactive time not met`.

   **`-dr` is not read-only.** That dry run wrote real per-torrent share limits (4 at ratio 1.2 and
   15 days, 3 at 1.0 and 10 days, 15 explicitly unlimited) plus its own `~share_limit_*` and
   `LastActiveLimitNotReached` tags. Nothing was ever near a limit and the global action is Pause,
   not Remove, so nothing was at risk, and all 46 torrents were put back to `(-2, -2, -2)`. Two
   things to remember: preview the cutover from the tool's log rather than by enabling
   `share_limits`, and resetting a limit on qBittorrent 5.2 needs all four of `ratioLimit`,
   `seedingTimeLimit`, `inactiveSeedingTimeLimit` and `shareLimitAction` or the API answers 400.

   And one that cross-seed introduced: its injected torrents land in a `cross-seed-link` category
   that no list here mentions, so they are outside `nohardlinks` too. That is correct for now (they
   are hardlinks by definition), but the category has to be accounted for before `share_limits` is
   allowed to delete anything.

3. **What cross-seed changes about deletion**, now that it runs: a cross-seeded file has a link
   count above one for as long as the second torrent exists, and `scripts/trackers/seed-cleanup.py` reads that as
   "the library still holds it" and keeps both forever. Nothing is lost and nothing is over-deleted,
   but bytes that used to be reclaimed after watching now stay until the cross-seed goes. That is
   the trade for free ratio, and it is another reason the deleting side belongs in qbit_manage,
   which understands cross-seeds explicitly.
4. **autobrr**, planned last but **done first, on 2026-08-21**, because the ratio was the thing
   under a deadline and this is what fixes it: it reacts to a tracker's IRC announce in seconds
   rather than the minutes an RSS poll costs, which is the difference between being an early seeder
   and arriving to a swarm that already has thirty. Concurrency is capped at one download by the
   client rule, not by the filter. Full configuration and the two API traps are in
   `docs/private-trackers.md`. What is left is one filter per remaining tracker, which waits on
   knowing each one's rules.

The one thing `scripts/trackers/seed-cleanup.py` does that qbit_manage cannot is ask Radarr and Sonarr what a
download actually produced, which mattered for RAR releases. That reason disappeared on 2026-08-21
when RAR packs became a hard reject.

And Jackett is deliberately **not** part of this: it is Prowlarr's predecessor, Prowlarr is already
here, and both cross-seed and autobrr integrate with Prowlarr directly.
