# RPi HomeServer — Claude Code Context

## What this project is

A modular Docker-based home server on Raspberry Pis. All services run as Docker containers managed by
Docker Compose. The repo is the source of truth: a push to `main` deploys within seconds via a GitHub
webhook, with a cron every 30 minutes on each node as the fallback (`scripts/deploy/apply.sh`
handles both). Overview and doc map: [README.md](README.md).

**Two-repo architecture:** Both repos are public. The split is *generic vs personal*, not private vs
public: this repo is meant to be clonable and useful to anyone, so nothing personal lives here.
Personal/custom services (Telegram bots, the Fantasy panel, torre) live in
[`rpi-services`](https://github.com/PlatanosVerdes/rpi-services). Both share `media-network`, are
deployed by the same script, and extend the same Caddy instance (see Networking below).

**Conventions both repos must follow** (they are one system, so drift hurts):
- Image/app versions in a committed `versions.env`, never `:latest` and never inline in compose.
- One deploy script, `rpi-homeserver/scripts/deploy/apply.sh`, which deploys both repos.
- Cron: each repo owns its own `scripts/crontab` **fragment**, holding only its own jobs.
  `scripts/setup/install-crontab.sh` merges them and installs the result on every deploy (the
  rpi-services fragment is optional), the same way Caddy imports `config/caddy/services/`.
- Secrets in each repo's own `.env`, gitignored, mirrored in `.env.example`.

---

## Where to look

```
docker-compose.yml, compose-{core,media,arrs,mon}.yml   Services; `include`s the four modules
overrides/secondary-node.yml   Only for a second node, through COMPOSE_FILE
versions.env                   Every image version and services/ image tag (committed)
config/                        Committed config: Caddy, Prometheus, Grafana (dashboards + alerting),
                               arr formats/profiles, qbit-manage, qBittorrent, trackers, cross-seed,
                               autobrr/Maintainerr exports, Homepage, Vector, logrotate, udev
services/                      Code for the images CI builds and the Pi pulls; deploy-webhook (systemd)
scripts/deploy/apply.sh        The deploy pass
scripts/{setup,sync}/          Host state and app config the pass converges every time
scripts/{ops,recovery,ci}/     Scheduled jobs, by-hand recovery, GitHub Actions helpers
scripts/crontab                This repo's cron fragment (merged with the companion repo's)
appdata/                       Container state, not in git, backed up nightly
```

What each script does: [docs/architecture.md](docs/architecture.md#the-scripts). What may overwrite
which app's config, and what git does not have: the same file.

---

## Nodes

- **raspi** runs `COMPOSE_PROFILES=all` plus `tunnel`. **streamhalla** runs a per-service list with
  `COMPOSE_FILE=docker-compose.yml:overrides/secondary-node.yml`. Both run `main` with the same
  `versions.env`; there are no per-node branches or pins.
- Each node has its own `*/30` cron running `apply.sh`; only the raspi has the webhook.
- **Never assume a service runs everywhere.** A new deploy step for one service is gated with
  `runs <service>` in `apply.sh` (today: the Grafana render, the qbit-manage copy, config-export) or
  skips itself when its app is absent, as the sync scripts do; a cron job for one gets a
  `# when: <service>` line above it in `scripts/crontab` (or `# when: file <path>`).
- Every service gets a profile named after itself plus its group and `all`. Profiles are not part of
  the config hash, so adding one recreates nothing.
- Anything only a second node needs goes in `overrides/secondary-node.yml`, never as a change to the
  main node's containers. What the raspi reads from streamhalla (Prometheus datasource, Caddy routes,
  sleipnir peer) is wired in the companion repo and `.env`, not here.

Details: [docs/nodes.md](docs/nodes.md).

---

## Secrets

Secrets live in **`.env`** as plain variables, which compose and every script read. On both nodes
that file is written by the deploy from an encrypted copy in the private `homelab-secrets` repo
(SOPS + age, one directory per node, [docs/secrets.md](docs/secrets.md)): **never edit `.env` on a
Pi**, change the secret there, or the next pass puts the old value back. Elsewhere, copy
`.env.example` to `.env`. Every new variable also goes in `.env.example`, with a comment.
Bitwarden Secrets Manager was tried and dropped; do not reintroduce it
([docs/decisions.md](docs/decisions.md)).

---

## Deploy rules

How the pass works, step by step: [docs/deploy.md](docs/deploy.md). The rules that follow from it:

- **The pass is idempotent** and runs every 30 minutes whatever happened. Anything it converges must
  be safe to apply twice; anything done by hand on a Pi that the pass manages is undone by it.
- **Only a changed image recreates a container**: never add `--build` to `up`, and keep
  `BUILDX_NO_DEFAULT_ATTESTATIONS=1`. A recreate cuts Caddy's connections and the stream Jellyfin
  reads from bifrost.
- **A route change is a `caddy reload`, never a rebuild or restart.** Config read once at startup
  (Grafana alerting, Homepage `settings.yaml`, Vector, Prometheus, blackbox) is restarted by the pass
  when the pull touched it; a new file of that kind needs the same treatment in `apply.sh`.
- **`apply.sh` re-execs itself** when a pull changes it, because bash reads the file as it runs.
- **Sync scripts run after compose** (the apps must be up) and before the metrics push. Config that
  lives only in an app's database is pushed from git (`scripts/sync/`) unless a wrong push would
  delete data; then it is exported and only checked for drift (`config-export.py --check`).
- **One lock, coalescing**: a second run leaves `.deploy.pending` and the holder runs once more.
  A failed build is retried by later passes up to five times.

---

## Networking

- **Local LAN:** Caddy listens for HTTP short names (`http://jellyfin`, `http://grafana`, etc.). Clients need an entry in `/etc/hosts` pointing `STATIC_IP` to those names.
- **Remote (Tailscale):** All HTTPS subdomains (`*.platanosverdes.com`) resolve to the Pi's Tailscale IP (`TAILSCALE_IP`). Certificates issued automatically via Cloudflare DNS challenge.
- **Pi-hole:** DNS for the whole tailnet. Every `*.platanosverdes.com` host in the Caddyfile points to `TAILSCALE_IP` in Pi-hole's custom DNS (written by `scripts/sync/pihole-dns.sh`, never by hand), except those in `PIHOLE_DNS_PUBLIC_HOSTS` (Grafana), which resolve to Cloudflare Access even on the tailnet.
- **Docker network:** All services share `media-network` (bridge), except the host-network ones listed in [docs/architecture.md](docs/architecture.md#the-network).
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

### Public side

- Cloudflare Tunnel `rpi` (`cloudflared`, profile `tunnel`, opt-in, not in `all`) carries the deploy
  webhook and the apps published behind Cloudflare Access. Nothing on the router is forwarded for it.
- **A new public app gets its Access application first**, then the tunnel route, then the proxied DNS
  record, and a `prometheus.probe.access` label with its public URL. Use one-level hostnames
  (`a-b.<domain>`): Cloudflare's free certificate does not cover `a.b.<domain>`.
- Never a catch-all hostname on the tunnel. The webhook is the one thing public without Access
  (GitHub cannot log in); its guard is the HMAC check.
- Video never goes through Cloudflare. Media players stay on the tailnet and the LAN.
- This repo is public: no account, zone, tunnel or Access application IDs, no AUDs, no emails, no
  addresses beyond what is already here. Those live in `.env` and the Cloudflare dashboard.

Details: [docs/access.md](docs/access.md).

---

## Adding a new service

See [docs/add-service.md](docs/add-service.md) for the step-by-step guide. Short version:
1. Add the service to the appropriate `compose-*.yml`, with `profiles: [<name>, <group>, all]` and a
   `prometheus.probe` label holding the URL its liveness probe should hit (that label is the whole
   monitoring setup; leave it out only if the service serves no HTTP). No `ports:` unless something
   on the host needs it, and then on `127.0.0.1`; on the host network, a `host.ports` label instead
   (CI checks both repos for clashes)
2. Add HTTPS route to `config/caddy/Caddyfile`; Pi-hole DNS follows on its own (`pihole-dns.sh`)
3. Add to Homepage `config/homepage/services.yaml` (optional)
4. Built from `services/`? Give it an `image: ghcr.io/platanosverdes/rpi-homeserver-<name>:${<NAME>_IMAGE_TAG}`
   line beside `build:` and `<NAME>_IMAGE_TAG=unbuilt` in `versions.env`, and CI builds and pins it
   ([docs/service-images.md](docs/service-images.md))
5. Deploy steps or cron jobs for it get their `runs` / `# when:` guard (see Nodes)

---

## Services with their own code

| Service | Where its code is | Notes |
| :--- | :--- | :--- |
| bifrost | [PlatanosVerdes/bifrost](https://github.com/PlatanosVerdes/bifrost), pinned as `BIFROST_VERSION` | AceStream list and the proxy Jellyfin plays from. Serves `/channel/<slug>`, `/api/channels` (farnsworth reads it) and `/api/status`. The rules it must keep are in its README |
| sleipnir, huginn | [PlatanosVerdes/sleipnir](https://github.com/PlatanosVerdes/sleipnir), `SLEIPNIR_VERSION` | One image run twice (`huginn` via `command`). Tunnels opt in with `sleipnir.enable` ([docs/sleipnir.md](docs/sleipnir.md)) |
| muninn | [PlatanosVerdes/muninn](https://github.com/PlatanosVerdes/muninn), `MUNINN_VERSION` | Who uses each bot. Bots opt in with a `muninn.stats` label (their `/bot-stats` URL, on an unpublished port: it holds names; Go bots use `github.com/PlatanosVerdes/gonana/botstats`) and join the internal `muninn` network (`networks: muninn: external: true`), so a public bot like torre stays off media-network. Shares docker-socket-proxy with sleipnir on `docker-api` |
| everything in `services/` | here, built by CI ([docs/service-images.md](docs/service-images.md)) | A source change deploys when CI pins the new `<NAME>_IMAGE_TAG`, minutes after the push. `bash scripts/recovery/rebuild-service.sh <name>` builds it on the Pi instead |

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

**Grafana provisioning never deletes anything on its own.** Removing an alert rule is two edits: out
of `groups` and its uid under `deleteRules`. A dashboard UID that existed and was deleted cannot be
provisioned again on v13: give a revived dashboard a new UID. A failed alerting provisioning takes
all of Grafana down. See [docs/alerting.md](docs/alerting.md#provisioning-traps).

Community dashboards to import manually: `1860` (Node Exporter Full), `193` (cAdvisor).

---

## Documentation

`docs/` holds one topic per file, written as guides someone can follow without having been here
when it was built.

**A doc change is rarely only a doc change.** Before calling it done, check what else points at it:

| If you | Also check |
| :--- | :--- |
| Add a file to `docs/` | It is linked from `README.md`, in the section it belongs to. An unlinked doc is one nobody finds |
| Rename or delete a doc | `git grep -n '<old-name>.md'` across the whole repo: docs, CLAUDE.md, and the code comments, compose files and dashboards that point at docs. Cross-links break silently |
| Decide against an idea | A short entry in [docs/decisions.md](docs/decisions.md) with the reason, not a section in PENDING.md |
| Add a service | Its compose module line in README's "What runs", `versions.env`, the profile tables in [docs/nodes.md](docs/nodes.md), and the checklist in [docs/add-service.md](docs/add-service.md) |
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
