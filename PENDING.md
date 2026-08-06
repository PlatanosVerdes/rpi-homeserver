# Pending tasks

## ❌ Bitwarden Secrets Manager — dropped

Tried moving `.env` secrets to [Bitwarden Secrets Manager](https://bitwarden.com/products/secrets-manager/)
but it did not fit the workflow, so it was abandoned. Secrets stay in `.env` (gitignored).

`scripts/bws-run.py` and [docs/secrets-manager.md](docs/secrets-manager.md) are kept only as
a reference in case it is revisited. They are NOT wired into deploy.

---

## ✅ Pi-hole monitoring in Grafana — DONE

`pihole-exporter` running, scraping `host.docker.internal:8081`. Dashboard imported (uid `Pi-hole-Exporter`).

---

## ✅ Tailscale monitoring in Grafana — DONE (a different way)

Tailnet name: **Bannet**. This was solved without the `tailscale-exporter` container: the Go binary
in [services/tailscale-metrics](services/tailscale-metrics) runs from host cron every minute and
writes `tailscale.prom` to node_exporter's textfile collector, which Prometheus already scrapes.
`TAILSCALE_API_KEY` is in `.env` and the dashboard is
[config/grafana/dashboards_json/infrastructure/tailscale.json](config/grafana/dashboards_json/infrastructure/tailscale.json).

The commented-out `tailscale-exporter` service in [compose-mon.yml](compose-mon.yml) and its
commented scrape job in [config/prometheus/prometheus.yml](config/prometheus/prometheus.yml) are
the abandoned approach and can be deleted.

---

## Update alert: notify when a service has a newer, non-breaking version (not implemented)

**Goal:** a Grafana alert that fires only when a `versions.env` pin is behind upstream **and**
the versions in between carry no documented breaking change, so it never nags about the ones
that need a manual look.

**2026-08-05 snapshot: all 12 safe updates applied and verified, one by one, lowest to highest
risk.** Each was bumped in its own commit, deployed via the webhook, and checked on the Pi
(container healthy, logs clean, functional smoke test) before moving to the next:

| Service | Was | Now | Notes |
| :--- | :--- | :--- | :--- |
| pushgateway | v1.11.2 | v1.11.3 | |
| node_exporter | v1.11.1 | v1.12.1 | |
| cadvisor | v0.55.1 | v0.60.5 | `gcr.io/cadvisor/cadvisor` (the old mirror) has nothing past v0.55.1; `compose-mon.yml` now points at `ghcr.io/google/cadvisor`, the registry cadvisor's own README says to use since v0.53.0 |
| flaresolverr | v3.4.6 | v3.5.0 | |
| prometheus | v3.9.1 | v3.13.2 | |
| homepage | v1.10.1 | v1.13.2 | |
| speedtest-tracker | v1.13.10-ls138 | v1.14.7-ls165 | |
| maintainerr | 3.18.0 | 3.21.1 | |
| qbittorrent | 5.1.4-r2-ls442 | 5.2.3_v2.0.13-ls469 | LinuxServer's tag format for this image now embeds the libtorrent version |
| jellyfin | 10.11.6ubu2404-ls21 | 10.11.11ubu2604-ls43 | base OS moved Ubuntu 24.04 -> 26.04 |
| pihole | 2026.02.0 | 2026.07.2 | DNS/gravity DB auto-migrated v21->v22 on first boot; verified both `*.platanosverdes.com` resolution and ad-blocking after |
| caddy | 2.10.2 | 2.11.4 | checked the v2.11.4 security-patch caveat (backslash path normalization, stripHTML, underscore-headers) against `config/caddy/Caddyfile` — only plain `reverse_proxy` blocks, none of it applies |

**grafana: 12.3.3 -> 13.1.2, done separately on 2026-08-06** after actually reading the v13.0
upgrade guide's breaking-changes list end to end and checking each one against this instance:

- numeric datasource-id APIs disabled by default — not used, provisioning and every dashboard
  already reference Prometheus by `uid`.
- `grafana-cli`/`grafana-server` removed — not used anywhere in this repo.
- Image Renderer plugin removed — not installed, no screenshots/reports configured.
- legacy Alertmanager API endpoints removed/restricted — alerting is entirely file-provisioned
  (`rules.yml`, `policies.yml`, the contact-point template), no script calls those endpoints.
- tightened RBAC — single org, default admin user only, no custom roles.
- the one-time, irreversible-without-a-restore unified-storage migration — ran `scripts/backup.sh`
  by hand right before deploying, so a fresh `appdata` backup existed regardless of the daily cron.

Landed on v13.1.2 directly rather than v13.0.0 (which shipped a real migration bug for Git Sync
users, irrelevant here since Git Sync isn't used, but no reason to land on the known-bad tag) and
skipped the "bump to latest 12.x patch first" step from Grafana's own guide (that step exists to
de-risk plugin compatibility before the React 19 jump; this instance has zero custom plugins).
Migration logs confirmed a clean run: 6 folders + 14 dashboards migrated, counts validated, zero
rejected.

| Service | Verdict |
| :--- | :--- |
| cloudflared, overseerr, prowlarr, radarr, sonarr, bazarr, blackbox-exporter | already current as of 2026-08-05 |
| aceserve, qbittorrent-exporter, pihole6_exporter | pinned by digest, no version to check at all |
| plex | closed source, no public releases to check against |

Re-run the check below periodically — this table goes stale as soon as upstream ships again.

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
