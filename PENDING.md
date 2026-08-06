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

## Plex casting/sharing from same-network devices (not investigated)

**Goal:** figure out how to cast/share to Plex from other devices on the same LAN (phone, TV,
whatever) — the whole "Chromecast-style" flow, not just opening the web player. Not researched
yet, no plan exists. Revisit when there's time to dig into it.

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
