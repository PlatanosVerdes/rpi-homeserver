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
compose-core.yml            Caddy, Homepage, Pi-hole, Speedtest-tracker, cloudflared
compose-media.yml           Plex, Jellyfin, Seerr, Bazarr, Maintainerr, Tautulli, watch-next,
                            subtitle-links, audio-convert; AceStream: gluetun, aceserve, kubo,
                            bifrost, farnsworth; sleipnir, gluetun-test, huginn, muninn,
                            docker-socket-proxy
compose-arrs.yml            Prowlarr, Radarr, Sonarr, qBittorrent, FlareSolverr, Unpackerr,
                            cross-seed, autobrr, qbit-manage, tracker-control, torrent-drop
compose-mon.yml             Prometheus, Grafana, Pushgateway, node-exporter, cAdvisor,
                            blackbox-exporter, pi-metrics, qbittorrent-exporter, pihole-exporter,
                            tailscale-metrics, VictoriaLogs + Vector (see docs/logging.md)
overrides/secondary-node.yml  Extra settings for a second node, loaded through COMPOSE_FILE

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
  qbit-manage/config.yml    Tags and share-limit groups: the seed goal per tracker. Copied into
                            appdata by the deploy
  qbittorrent/              preferences.json, pushed by scripts/sync/qbit-config.sh
  trackers/                 Each private tracker's rules and tiers, read by tracker-control
  autobrr/, maintainerr/    Exported by scripts/ops/config-export.py; the deploy only checks drift
  cross-seed/, blackbox/    cross-seed's config.js; blackbox probe modules
  logrotate/, udev/         Host files installed by scripts/setup/ on every deploy

services/                   Source code for custom services built in this repo. CI builds each
                            one compose builds and publishes it to GHCR; the Pi pulls it
                            (docs/service-images.md)
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
  audio-convert/            Go service: page listing the files a client had its audio re-encoded
                            on, with pi-metrics' plan, and a button running audio-compat.py --file
  tracker-control/          Go service: reads each tracker account and moves the freeleech filter,
                            requiredFlags and the grab rate from its headroom. Was two crons
  farnsworth/               Go service: page that hands an AceStream channel to VLC

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
    arr-links.sh            Wires Seerr and Bazarr to Radarr/Sonarr
    bazarr-prune-subs.py    Drops Bazarr's subtitle rows left behind by a replaced file
    pihole-dns.sh           Caddy's *.platanosverdes.com hosts -> Pi-hole custom DNS
    plex-prefs.sh           Plex LAN networks and the 95% played threshold
    qbit-config.sh          qBittorrent queue limits, upload cap and BT port
    jellyfin-guide.sh       Jellyfin's XMLTV guide -> the copy bifrost cuts to our channels
    audio-compat.py         Nightly: an AC3 track for files whose audio codec a client refused.
                            --file converts one, and is what audio-convert runs; --scan lists
                            what the nightly pass would convert

  ci/                       Run by GitHub Actions
    ports.py                Host ports both repos' containers take; fails on a clash
    service-images.py       Which services/ images are due, and their pins in versions.env

  ops/                      Everything else on a schedule
    backup.sh               Daily appdata backup, pushes metrics to Grafana
    heartbeat.sh            Dead man's switch ping to an external check, every minute
    cutoff-search.sh        *arr search for missing and below-cutoff items. Out of the cron
                            since 2026-08-24, being retired (PENDING.md)
    oci-hunt.py             Keeps asking Oracle for the two free instances until capacity exists
    indexer-retry.py        Clears Prowlarr's backoff once a blocked indexer's site answers again
    config-export.py        Pulls autobrr's filters and Maintainerr's rules into git; --check on deploy

appdata/                    Persistent container data (NOT in git, lives on disk)
.github/workflows/          ports.yml (host port clashes), service-images.yml (build services/,
                            publish, pin), bump-app-versions.yml (pin an app repo's release)
docs/                       Setup guides
  architecture.md           What talks to what, and what may overwrite which app's config
```

---

## Secrets — current state

Secrets live in **`.env`** as plain variables, which compose and every script read. On the Pis that
file is written by the deploy from an encrypted copy in the private `homelab-secrets` repo (SOPS +
age, one directory per node, see [docs/secrets.md](docs/secrets.md)): change a secret there, not in
the Pi's `.env`, or the next pass puts the old value back. Elsewhere, copy `.env.example` to `.env`
and fill in values.

```bash
docker compose up -d
```

> **Bitwarden Secrets Manager was evaluated and dropped**; SOPS + age is what is used. See
> PENDING.md for why, and do not reintroduce it without reading that first.

---

## How auto-deploy works

`scripts/deploy/apply.sh` runs on every GitHub push (webhook) and every 30 minutes via cron:
0. If `~/homelab-secrets` is a git checkout and `sops` is installed, pulls it and decrypts
   `<node>/rpi-homeserver.env` and `<node>/rpi-services.env` over each repo's `.env`, only when the
   result differs (docs/secrets.md). Then it takes the lock (below).
1. `git pull origin main`. If the pull changed `apply.sh` itself, it `exec`s the new version (bash
   reads the file as it runs, so the old code would carry on otherwise), passing the pre-pull
   commit along so the new run still sees what changed
2. **Renders Grafana's alerting config** (`render_grafana_alerting`): compose only interpolates
   `${VAR}` inside its own YAML, never inside a config file, so something has to combine the
   template in git with the secrets in `.env` before Grafana starts. That is this step. It writes
   `config/grafana/alerting/*` into `appdata/grafana-alerting/` with the values filled in, which
   is what Grafana actually mounts. See [docs/alerting.md](docs/alerting.md).
3. If HEAD changed → for each service compose builds (read from compose, so `services/*`):
   - one with a published image (`image: ghcr.io/...:${<NAME>_IMAGE_TAG}` beside its `build:`) is
     **pulled**, and only when its pinned tag is not on the Pi yet. GitHub Actions builds it and
     commits the new pin ("Deploy <service> <tag>"); that commit is what deploys it, not the source
     push. A failed pull falls back to building it here. See [docs/service-images.md](docs/service-images.md).
   - one without is **built** when its local build context holds a changed file, or when
     `versions.env` or a compose file changed.

   Then `docker compose up -d --remove-orphans` without `--build`.
   A recreate cuts what that container was serving — Caddy's live connections, the live stream
   Jellyfin is reading from `bifrost` — so only a container whose image actually changed may be
   recreated. Three things make that hold: a pinned tag moves only when that service's directory or
   build args changed; `--build` recreates every built container even when its image is identical;
   and BuildKit's default provenance attestation is timestamped, so a cached rebuild still got a new
   id, hence the deploy exports `BUILDX_NO_DEFAULT_ATTESTATIONS=1`. Route changes in `config/caddy/`
   are applied by `caddy reload`, never a rebuild. `DEPLOY_BUILD_LOCAL=1` builds the published
   services from source on this pass instead, for when CI or GHCR is down.
4. If no change → `docker compose up -d --remove-orphans` (ensures containers are running)
5. Installs the merged host crontab (`scripts/setup/install-crontab.sh`), the logrotate policy and
   the udev rules (`install-logrotate.sh`, `install-udev.sh`)
6. Runs the sync scripts, which need the apps up, hence after compose: `arr-config.sh` (Radarr/Sonarr
   formats and profiles), `config-export.py --check` (autobrr/Maintainerr drift, logged only),
   `pihole-dns.sh` (Pi-hole records from the Caddyfile), `arr-links.sh` (Seerr/Bazarr), `plex-prefs.sh`
   (LAN Networks, 95% played), `qbit-config.sh` (qBittorrent limits and BT port), `jellyfin-guide.sh`
7. Prunes images and build cache once a day, then pushes metrics to Pushgateway (visible in Grafana
   "Deploy Monitor" dashboard)

Three steps are gated with `runs <service>` (is it in this node's `COMPOSE_PROFILES`?): the
Grafana render, the qbit-manage config copy and `config-export.py --check`. The other sync scripts
skip themselves when their app is absent. The Grafana, appdata-ownership, qbit-manage and webhook
steps run for rpi-homeserver only; `caddy reload` runs for both repos. The restarts for config read
once (webhook unit, Grafana, Caddy reload, Homepage, Vector, Prometheus, blackbox) happen before the
image step, and a failed pull still runs `up -d`.

Order matters: the render sits after the pull (or it would use a stale template) and before
compose (or Grafana would start reading the previous file).

It takes an `flock` on `.deploy.lock`, so a burst of pushes cannot start two deploys at once. A run
that cannot get the lock leaves `.deploy.pending` and exits; the holder runs once more on its way
out (coalescing, not a queue).

**Push-triggered deploy:** GitHub webhook → Cloudflare tunnel → `services/deploy-webhook`
(systemd, HMAC-verified) → this script. Setup in [docs/deploy-webhook.md](docs/deploy-webhook.md).
The cron stays as the self-heal net.

**Cron entry**, from `scripts/crontab`. `install-crontab.sh` replaces `@PROJECT_DIR@` with this
machine's checkout and drops a job whose `# when: <service>` line names a service this node does not
run:
```
*/30 * * * * @PROJECT_DIR@/scripts/deploy/apply.sh >> @PROJECT_DIR@/apply.log 2>&1
```

---

## Networking

- **Local LAN:** Caddy listens for HTTP short names (`http://jellyfin`, `http://grafana`, etc.). Clients need an entry in `/etc/hosts` pointing `STATIC_IP` to those names.
- **Remote (Tailscale):** All HTTPS subdomains (`*.platanosverdes.com`) resolve to the Pi's Tailscale IP (`TAILSCALE_IP`). Certificates issued automatically via Cloudflare DNS challenge.
- **Pi-hole:** DNS for the whole tailnet. Every `*.platanosverdes.com` host in the Caddyfile points to `TAILSCALE_IP` in Pi-hole's custom DNS (written by `scripts/sync/pihole-dns.sh`), except those in `PIHOLE_DNS_PUBLIC_HOSTS` (Grafana), which resolve to Cloudflare Access even on the tailnet.
- **Public side:** Cloudflare Tunnel `rpi` (`cloudflared`, profile `tunnel`) carries the deploy webhook and the apps published behind Cloudflare Access. Nothing on the router is forwarded for it.
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
| `moni` | Prometheus, Grafana, Pushgateway, node-exporter, cAdvisor, blackbox-exporter, pi-metrics, qbittorrent-exporter, pihole-exporter, tailscale-metrics, VictoriaLogs, Vector, Speedtest-tracker |
| `acestream` | Aceserve, gluetun, bifrost, Kubo, Jellyfin, farnsworth + Grafana/Prometheus/Pushgateway |
| `media` | Plex, Seerr, Prowlarr, Radarr, Sonarr, qBittorrent, FlareSolverr, Unpackerr, Bazarr, Maintainerr, Tautulli, watch-next, subtitle-links, torrent-drop, audio-convert, qbit-manage, autobrr, tracker-control, cross-seed |
| `vpn` | sleipnir, docker-socket-proxy, gluetun-test, huginn (see docs/sleipnir.md) |
| `bots` | muninn, docker-socket-proxy |
| `tunnel` | Cloudflared: the deploy webhook and the apps behind Cloudflare Access (docs/deploy-webhook.md) |
| `all` | Everything except `tunnel` (it needs a token, so it is opt-in) |

Every service also has a profile named after itself, so a node that runs a subset lists exactly
what it runs instead of taking a whole group: e.g. streamhalla runs
`COMPOSE_PROFILES=jellyfin,gluetun,aceserve,kubo,bifrost,sleipnir,docker-socket-proxy,speedtest-tracker,prometheus,pushgateway,node-exporter,cadvisor`.
Profiles are not part of a container's config hash, so adding one recreates nothing.

Main Pi: `COMPOSE_PROFILES=all` plus `tunnel`. streamhalla: the list above, with
`COMPOSE_FILE=docker-compose.yml:overrides/secondary-node.yml`. Both run `main` and deploy themselves
from their own cron; only the main Pi has the webhook.

---

## Adding a new service

See [docs/add-service.md](docs/add-service.md) for the step-by-step guide. Short version:
1. Add service to the appropriate `compose-*.yml`, with a `prometheus.probe` label holding the
   URL its liveness probe should hit (that label is the whole monitoring setup; leave it out only
   if the service serves no HTTP). No `ports:` unless something on the host needs it, and then on
   `127.0.0.1`; on the host network, a `host.ports` label instead (CI checks both repos for clashes)
2. Add HTTPS route to `config/caddy/Caddyfile`
3. Pi-hole DNS is automatic: `scripts/sync/pihole-dns.sh` adds the record from the Caddyfile route
4. Add to Homepage `config/homepage/services.yaml` (optional)
5. Built from `services/`? Give it an `image: ghcr.io/platanosverdes/rpi-homeserver-<name>:${<NAME>_IMAGE_TAG}`
   line beside `build:` and `<NAME>_IMAGE_TAG=unbuilt` in `versions.env`, and CI builds and pins it
   ([docs/service-images.md](docs/service-images.md))

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

A Go source change deploys once CI has published the image and pinned `WATCH_NEXT_IMAGE_TAG`, a few
minutes after the push (see [docs/service-images.md](docs/service-images.md)). To build it on the Pi
instead: `bash scripts/recovery/rebuild-service.sh watch-next`.

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
as a build arg. The `*_IMAGE_TAG` lines are the images built from `services/`, and the Service
images workflow writes them, not a person (docs/service-images.md).

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