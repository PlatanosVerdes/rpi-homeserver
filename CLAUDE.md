# RPi HomeServer — Claude Code Context

## What this project is

A modular Docker-based home server running on a Raspberry Pi. All services run as Docker containers managed by Docker Compose. The repo is the source of truth: a push to `main` deploys within seconds via a GitHub webhook, with a cron every 30 minutes as the fallback (`scripts/deploy/apply.sh` handles both).

**Two-repo architecture:** Both repos are public. The split is *generic vs personal*, not private vs public: this repo is meant to be clonable and useful to anyone, so nothing personal lives here. Personal/custom services (Telegram bots, the Fantasy panel, torre) live in [`rpi-services`](https://github.com/PlatanosVerdes/rpi-services). Both run on the same Pi, share `media-network`, are deployed by the same script, and extend the same Caddy instance via the services import mechanism (see Networking section below).

**Conventions both repos must follow** (they are one system, so drift hurts):
- Image/app versions in a committed `versions.env`, never `:latest` and never inline in compose.
- One deploy script, `rpi-homeserver/scripts/deploy/apply.sh`, which deploys both repos.
- Cron: each repo owns its own `scripts/crontab` **fragment**, holding only its own jobs.
  `scripts/setup/install-crontab.sh` merges them and installs the result on every deploy (the
  rpi-services fragment is optional), the same way Caddy imports `config/caddy/services/`.
- Secrets in each repo's own `.env`, gitignored, mirrored in `.env.example`.

---

## Repository layout

```
docker-compose.yml          Entry point — uses `include` to load all modules
versions.env                Single source of truth for all image versions (committed)
compose-core.yml            Caddy, Homepage, Pi-hole, Speedtest-tracker
compose-media.yml           Plex, Jellyfin, Overseerr, Acestream, Kubo, Tautulli, watch-next
compose-arrs.yml            Prowlarr, Radarr, Sonarr, qBittorrent, FlareSolverr, Unpackerr,
                            cross-seed, autobrr, qbit-manage, torrent-drop
compose-mon.yml             Prometheus, Grafana, Pushgateway, node-exporter, cAdvisor,
                            VictoriaLogs + Vector (container logs, see docs/logging.md)

config/                     Static config files committed to git
  caddy/Caddyfile           Reverse proxy rules (HTTPS + HTTP short names)
  caddy/services/           Extra route files for this repo (auto-imported by Caddy)
  caddy/ext-services/       Mount point for a companion repo's routes (see EXT_CADDY_PATH)
  vector/ext-logs/          Mount point for a companion repo's cron logs (see EXT_LOGS_PATH)
  prometheus/prometheus.yml Scrape jobs (the blackbox targets come from Docker labels)
  grafana/                  Provisioned datasources + dashboard JSONs
  grafana/alerting/         Alert rules, policy, contact-point template (rendered on deploy)
  homepage/                 Dashboard YAML configs
  vector/vector.yaml        What Vector collects and where it ships it
  arr/radarr, arr/sonarr    Custom formats + quality profiles, pushed into each app on deploy
                            (scripts/sync/arr-config.sh) — otherwise they only exist in appdata

services/                   Source code for custom services built in this repo
  caddy/                    Custom Caddy image with the Cloudflare DNS plugin. Not next to the
                            Caddyfile: a build context there made every route change a rebuild
  tailscale-metrics/        Go service, Dockerized — Tailscale peer metrics for Prometheus
  deploy-webhook/           Python receiver (systemd) — deploys on GitHub push via Cloudflare tunnel
  watch-next/               Go service: monitors + searches the next Sonarr episode(s) on watch
  subtitle-links/           Go service: page listing movies/episodes with a downloadable text subtitle
  pi-metrics/               Go exporter: the numbers no exporter provides (upgrades, torrents,
                            indexer usage, where the disk went, what zram costs, and what Plex or
                            Jellyfin is re-encoding right now). Was three crons
  torrent-drop/             Go service: page to add a torrent by hand, and the immediate
                            cross-seed search on every torrent that completes
  tracker-control/          Go service: reads each tracker account and moves the freeleech filter,
                            requiredFlags and the grab rate from its headroom. Was two crons

scripts/                    Operational scripts, grouped by what they do
  crontab                   This repo's cron fragment, merged with the other repo's on deploy

  deploy/                   Runs on every push and every 30 min
    apply.sh                The orchestrator: pull, compose, then every sync below. Webhook + cron

  setup/                    Host state apply.sh converges on each pass: cron, log rotation, udev
    install-crontab.sh      Merges both repos' crontab fragments and installs them
    install-logrotate.sh    Installs the log rotation policy for both repos
    install-udev.sh         Installs config/udev/ rules (the media disks' readahead)

  recovery/                 Run by hand, after something broke
    rebuild-service.sh      Rebuild one compose service from scratch
    recovery-status.sh      After a rebuild: what still needs a human

  sync/                     Config that lives only in an app's appdata, pushed from git each deploy
    arr-config.sh           Custom formats and quality profiles -> Radarr/Sonarr
    arr-links.sh            Wires Overseerr and Bazarr to Radarr/Sonarr
    bazarr-prune-subs.py    Drops Bazarr's subtitle rows left behind by a replaced file
    pihole-dns.sh           Caddy's *.platanosverdes.com hosts -> Pi-hole custom DNS
    plex-prefs.sh           Plex LAN networks and the 95% played threshold
    qbit-config.sh          qBittorrent queue limits, upload cap and BT port
    jellyfin-guide.sh       Jellyfin's XMLTV guide -> the copy bifrost cuts to our channels
    audio-compat.py         Nightly: an AC3 track for files whose audio codec a client refused

  ci/                       Run by GitHub Actions
    ports.py                Host ports both repos' containers take; fails on a clash

  ops/                      Everything else on a schedule
    backup.sh               Daily appdata backup, pushes metrics to Grafana
    heartbeat.sh            Dead man's switch ping to an external check, every minute
    cutoff-search.sh        Nightly *arr search for missing and below-cutoff items
    oci-hunt.py             Keeps asking Oracle for the two free instances until capacity exists
    indexer-retry.py        Clears Prowlarr's backoff once a blocked indexer's site answers again
    config-export.py        Pulls autobrr's filters and Maintainerr's rules into git; --check on deploy

appdata/                    Persistent container data (NOT in git, lives on disk)
docs/                       Setup guides
  architecture.md           What talks to what, and what may overwrite which app's config
```

---

## Secrets — current state

Secrets currently live in **`.env`** as plain variables. Copy `.env.example` to `.env` and fill in values.

```bash
docker compose up -d
```

> **Bitwarden Secrets Manager was evaluated and dropped.** It did not fit the workflow, so
> secrets stay in `.env` (gitignored, never committed). `scripts/deploy/apply.sh` uses plain `docker compose`.
> See PENDING.md for why, and do not reintroduce it without reading that first.

---

## How auto-deploy works

`scripts/deploy/apply.sh` runs on every GitHub push (webhook) and every 30 minutes via cron:
1. `git pull origin main`
2. **Renders Grafana's alerting config** (`render_grafana_alerting`): compose only interpolates
   `${VAR}` inside its own YAML, never inside a config file, so something has to combine the
   template in git with the secrets in `.env` before Grafana starts. That is this step. It writes
   `config/grafana/alerting/*` into `appdata/grafana-alerting/` with the values filled in, which
   is what Grafana actually mounts. See [docs/alerting.md](docs/alerting.md).
3. If HEAD changed → `docker compose build` for **only** the services whose local build context
   holds a changed file (read from compose, so `services/*`), or all of them when `versions.env`
   or a compose file changed; then `docker compose up -d --remove-orphans` without `--build`.
   A recreate cuts what that container was serving — Caddy's live connections, the live stream
   Jellyfin is reading from `bifrost` — so only a container whose image actually changed may be
   recreated. Two things make that hold: `--build` recreates every built container even when its
   image is identical, and BuildKit's default provenance attestation is timestamped, so a cached
   rebuild still got a new id; the deploy exports `BUILDX_NO_DEFAULT_ATTESTATIONS=1`. Route
   changes in `config/caddy/` are applied by `caddy reload`, never a rebuild.
4. If no change → `docker compose up -d --remove-orphans` (ensures containers are running)
5. Installs the merged host crontab (`scripts/setup/install-crontab.sh`)
6. Converges Radarr/Sonarr's custom formats and quality profiles (`scripts/sync/arr-config.sh`),
   Pi-hole's `*.platanosverdes.com` DNS records (`scripts/sync/pihole-dns.sh`) and Plex's LAN Networks
   (`scripts/sync/plex-prefs.sh`) to what is committed — the apps must already be up, hence running
   after compose rather than before
7. Pushes metrics to Pushgateway (visible in Grafana "Deploy Monitor" dashboard)

Order matters: the render sits after the pull (or it would use a stale template) and before
compose (or Grafana would start reading the previous file).

It takes an `flock` on `.deploy.lock`, so a burst of pushes cannot start two deploys at once.

**Push-triggered deploy:** GitHub webhook → Cloudflare tunnel → `services/deploy-webhook`
(systemd, HMAC-verified) → this script. Setup in [docs/deploy-webhook.md](docs/deploy-webhook.md).
The cron stays as the self-heal net.

**Cron entry on the host:**
```
*/30 * * * * /home/raspi/rpi-homeserver/scripts/deploy/apply.sh >> /home/raspi/rpi-homeserver/apply.log 2>&1
```

---

## Networking

- **Local LAN:** Caddy listens for HTTP short names (`http://jellyfin`, `http://grafana`, etc.). Clients need an entry in `/etc/hosts` pointing `STATIC_IP` to those names.
- **Remote (Tailscale):** All HTTPS subdomains (`*.platanosverdes.com`) resolve to the Pi's Tailscale IP (`TAILSCALE_IP`). Certificates issued automatically via Cloudflare DNS challenge.
- **Pi-hole:** DNS for the whole tailnet. All `*.platanosverdes.com` subdomains point to `TAILSCALE_IP` in Pi-hole's custom DNS.
- **Docker network:** All services share `media-network` (bridge).
- **Plex is never proxied.** Remote playback is paid since April 2025 and the client decides remote
  vs local from the per-connection `local` flag Plex publishes to plex.tv, which only RFC1918
  addresses ever get. So Plex must be reached on `STATIC_IP`: Caddy `redir`s its two routes instead
  of proxying them, Homepage links to the same address, the Pi advertises its own LAN IPs as subnet
  routes so the tailnet can reach it, and `PLEX_LAN_NETWORKS` covers the server side. Do not "fix"
  any of this with a `reverse_proxy`. See [docs/plex-remote-access.md](docs/plex-remote-access.md).
- **Caddy extension from a companion repo:** the Caddyfile imports two globs,
  `config/caddy/services/*.caddy` (extra routes kept in this repo) and
  `/etc/caddy/ext-services/*.caddy` (routes owned by another repo). The second one is a bind mount
  driven by `EXT_CADDY_PATH` in `.env`:
  ```bash
  EXT_CADDY_PATH=/home/raspi/rpi-services/config/caddy
  ```
  Unset it and Caddy mounts an empty dir instead. This way rpi-services adds HTTPS routes without
  touching this repo, and the wiring lives in git rather than in a local override file.

  `EXT_LOGS_PATH` works the same way for logs: the companion repo's cron scripts append to `.log`
  files in its own root, and Vector collects them from `/host-logs/services` so they end up in
  VictoriaLogs alongside this repo's. Unset, it mounts `config/vector/ext-logs`, which is empty.
  Both repos' logs are keyed by file basename, so the two must not own a log with the same name.

  > A `ln -s ... config/caddy/services/rpi-services` symlink is documented in older notes and does
  > **not** work: the import glob is `*.caddy`, so a directory named `rpi-services` never matches.

---

## Profiles — selecting which services to run

Controlled via `COMPOSE_PROFILES` in `.env`. No need to touch compose files.

| Profile | Services |
| :--- | :--- |
| `essential` | Caddy, Homepage, Pi-hole, Speedtest-tracker |
| `moni` | Prometheus, Grafana, Pushgateway, node-exporter, cAdvisor, Pihole-exporter, Speedtest-tracker |
| `acestream` | Aceserve, gluetun, bifrost, Kubo, Jellyfin, farnsworth + Grafana/Prometheus/Pushgateway |
| `media` | Plex, Overseerr, Prowlarr, Radarr, Sonarr, qBittorrent, FlareSolverr, Unpackerr, Bazarr, Maintainerr, Tautulli, watch-next, torrent-drop |
| `bot` | Pol Academy Offers Bot |
| `vpn` | sleipnir, docker-socket-proxy, gluetun-test, huginn (see docs/sleipnir.md) |
| `bots` | muninn, docker-socket-proxy |
| `tunnel` | Cloudflared (publishes only the GitHub deploy webhook — see docs/deploy-webhook.md) |
| `all` | Everything except `tunnel` (it needs a token, so it is opt-in) |

Main Pi: `COMPOSE_PROFILES=all`. Secondary Pi: e.g. `COMPOSE_PROFILES=essential,moni`.

---

## Adding a new service

See [docs/add-service.md](docs/add-service.md) for the step-by-step guide. Short version:
1. Add service to the appropriate `compose-*.yml`, with a `prometheus.probe` label holding the
   URL its liveness probe should hit (that label is the whole monitoring setup; leave it out only
   if the service serves no HTTP). No `ports:` unless something on the host needs it, and then on
   `127.0.0.1`; on the host network, a `host.ports` label instead (CI checks both repos for clashes)
2. Add HTTPS route to `config/caddy/Caddyfile`
3. Add DNS record in Pi-hole pointing to `TAILSCALE_IP`
4. Add to Homepage `config/homepage/services.yaml` (optional)

---

## Custom services in `services/`

### bifrost (external image)
The AceStream list and the proxy Jellyfin plays from, in its own repo:
[PlatanosVerdes/bifrost](https://github.com/PlatanosVerdes/bifrost). Pinned as `BIFROST_VERSION`
in `versions.env`, never built here. It writes Jellyfin's M3U (one stable URL per channel) and a
guide cut down to those channels into `appdata/jellyfin/acestream/`, and serves `/channel/<slug>`,
`/api/channels` (farnsworth reads it) and `/api/status`. The rules it must keep, and how the engine
and gluetun around it are wired, are in its README.

### sleipnir (external image)
The gluetun tunnel panel and its exit check huginn, in their own repo:
[PlatanosVerdes/sleipnir](https://github.com/PlatanosVerdes/sleipnir). One image, pinned as
`SLEIPNIR_VERSION`, run twice (`huginn` via `command`). Tunnels opt in with the `sleipnir.enable`
label; see [docs/sleipnir.md](docs/sleipnir.md).

### muninn (external image)
Who uses each bot and how much traffic reaches it, in its own repo:
[PlatanosVerdes/muninn](https://github.com/PlatanosVerdes/muninn). Pinned as `MUNINN_VERSION`. Bots
opt in with a `muninn.stats` label (the URL of their `/bot-stats`, on an unpublished port: it holds
names); Go bots serve it with `github.com/PlatanosVerdes/gonana/botstats`. Shares
docker-socket-proxy with sleipnir on the internal `docker-api` network. Bots reach muninn, and
muninn them, on the internal `muninn` network: a bot joins it in its own compose
(`networks: muninn: external: true`), so a public bot like torre stays off media-network.

### tailscale-metrics
Go service, Dockerized, no published port. Prometheus scrapes `tailscale-metrics:9736/metrics`
over `media-network` (job `tailscale`).

Reads two sources: tailscaled's LocalAPI over the host socket, mounted read-only, for who is
online and the bytes per peer; and the Tailscale control API, for which peers are *approved*
exit nodes. `TAILSCALE_API_KEY` is optional and only fills `tailscale_peer_is_exit_node`.

It used to be a hand-compiled binary run from cron every minute, writing into node_exporter's
textfile collector. Nothing in git built it, so a rebuilt Pi lost the nine panels of the
Tailscale dashboard with no error anywhere. Being scraped rather than pushed also means its
death is visible: `up{job="tailscale"}` goes to 0.

### watch-next
Go service, Dockerized, no published port (reached by Tautulli/Jellyfin over `media-network` by
container name only). Monitors and searches the next `WATCH_NEXT_MARGIN` Sonarr episodes when
Tautulli or Jellyfin reports one watched, so a season fills in progressively instead of all at
once. Setup in [docs/watch-next.md](docs/watch-next.md).

Rebuild after Go source changes:
```bash
docker compose -f compose-media.yml up -d --build watch-next
```

---

## Key `.env` variables

```bash
TZ, STATIC_IP, TAILSCALE_IP   # Host config
PUID, PGID                     # File ownership (run: id -u && id -g)
DATA_ROOT                      # External disk mount (e.g. /mnt/data)
DATA_DB_ROOT                   # DB subdirectory
CONFIG_ROOT=./config
APP_CONFIG_PATH=./appdata
CF_API_TOKEN                   # Cloudflare DNS token for HTTPS certs
TAILSCALE_API_KEY              # Optional, only for tailscale_peer_is_exit_node
PLEX_LAN_NETWORKS              # Networks Plex treats as local (see Networking)
```

---

## Grafana dashboards

All dashboards are provisioned from JSON files in `config/grafana/dashboards_json/`. Changes to dashboards must be exported from Grafana and committed here — they are NOT persisted in the Grafana container's volume.

**Every new dashboard must be linked from the Home dashboard** (`dashboards_json/home.json`), or it
only exists for whoever remembers the URL. Home is one strip of four text panels — **Media,
Network, System, Automation** — each 6 wide and 8 tall, holding nothing but links. One line per
dashboard, one shape, no exceptions:

```markdown
<emoji> [Dashboard title](/d/<uid>/<slug>)
```

Rules that keep it readable:

- **The panel title is the group.** Never write a heading inside the content; that is what produced
  the three competing styles this replaced (a `##` that sometimes named a group and sometimes
  repeated the link directly below it).
- **The link text is the dashboard's real title**, not a description of it. What you read is what
  opens, and the two cannot drift apart. If the title is too vague to stand alone, rename the
  dashboard rather than papering over it here.
- **No descriptions.** If a group ever needs them to make sense, it has too many links and wants
  splitting instead.
- `uid` must match the dashboard's own `uid`; `slug` is its title lowercased with dashes.

Check the panel's `gridPos.h` still fits after adding a line: text panels do not grow, and the four
share a height, so bump all of them together and shift the `Secrets` row below.

Dashboard folders come from the directory structure (`foldersFromFilesStructure`), so
`dashboards_json/media/` is the Grafana folder "media". Put a dashboard where its subject lives, not
where the data comes from: `/mnt/data` usage is media, not infrastructure.

**Alerting** is provisioned too, from `config/grafana/alerting/` (rules, notification policy, and a
contact-point *template* for the Telegram bot). It is read-only in the Grafana UI on purpose. The
token and chat id come from `.env` via the deploy's render step, never from git. Full explanation
in [docs/alerting.md](docs/alerting.md).

Community dashboards to import manually:
- `1860` — Node Exporter Full
- `193` — cAdvisor

---

## Documentation

`docs/` holds one topic per file, written as guides someone can follow without having been here
when it was built.

**A doc change is rarely only a doc change.** Before calling it done, check what else points at it:

| If you | Also check |
| :--- | :--- |
| Add a file to `docs/` | It is linked from `README.md`, in the section it belongs to. An unlinked doc is one nobody finds |
| Rename or delete a doc | `grep -rn '<old-name>.md' README.md CLAUDE.md docs/` — cross-links between docs break silently |
| Add a service | The repository layout above, its compose module line, `versions.env`, and the checklist in [docs/add-service.md](docs/add-service.md) |
| Automate something that used to be manual | Every doc still walking the reader through it by hand. A guide describing a step the deploy now performs is worse than no guide, because it gets followed |
| Abandon an approach | Say so in the **title**, not in a note further down. A file list is read without opening anything |

Nothing enforces any of this; markdown has no tags or dependencies. The check is a loop:

```bash
for f in docs/*.md; do
    n=$(basename "$f")
    [ "$(grep -c "$n" README.md)" -eq 0 ] && echo "orphan: $n"
done
```

**Tone**, same as the rest of the repo:

- Write a guide, not a changelog. "Debian ships this commented out, so set it explicitly" rather
  than "this was empty and had grown to 2.9 MB".
- Define a term the first time it carries weight. RFC1918, CGNAT, tailnet, subnet router: a reader
  who does not already know them will not stop to look them up.
- Give the reason whenever a choice looks wrong from the outside: `/32` instead of a subnet,
  `redir` instead of `reverse_proxy`. Without it, the next person "fixes" it.
- No tutorial voice and no filler enthusiasm. A concrete question someone genuinely arrives with is
  useful; building up to a reveal is not.
- Say what a thing does **not** do. That is where the wasted debugging starts.

---

## Common operations

```bash
# Restart a service after config change
docker compose -f compose-core.yml restart caddy

# View live logs for a service
docker logs -f <container-name>

# Trigger deploy manually
bash scripts/deploy/apply.sh

# Rebuild from scratch (single service)
bash scripts/recovery/rebuild-service.sh <service-name>

# Run a backup manually
bash scripts/ops/backup.sh
```

## Image versions — centralized in `versions.env`

All image versions live in a single committed file, **`versions.env`** (the equivalent of a
`requirements.txt`). The `compose-*.yml` files reference them as `${SERVICE_VERSION}`, so
nothing uses `:latest`. This keeps deploys reproducible and prevents an upstream release from
silently breaking the stack on the next rebuild. Images with no published version tag
(aceserve, pihole6_exporter) are pinned by digest; Caddy's version is passed to its Dockerfile
as a build arg.

`versions.env` holds only versions (no secrets) so it IS tracked in git (`.gitignore` has a
`!versions.env` exception to the `*.env` rule).

**Loading:** compose reads it via `COMPOSE_ENV_FILES=versions.env,.env`. `scripts/deploy/apply.sh`
and `scripts/recovery/rebuild-service.sh` set this automatically. To run compose manually:
```bash
export COMPOSE_ENV_FILES=versions.env,.env
docker compose up -d
```

**To update a service:** bump its version in `versions.env`, commit, and let the auto-deploy
apply it (or `bash scripts/recovery/rebuild-service.sh <service>`). Check the running version first with
`docker inspect --format '{{.Config.Image}}' <container>`.