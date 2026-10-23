# Overview

The detail that used to open the README.

## Two nodes, three doors

![Two nodes and the three ways in](diagrams/two-nodes-three-doors.png)

| Node | Runs | Deploys on |
| :--- | :--- | :--- |
| raspi (main) | everything: `COMPOSE_PROFILES=all` plus the tunnel | the GitHub webhook and a 30-minute cron |
| streamhalla | the AceStream + Jellyfin subset and its own metrics, with `overrides/secondary-node.yml` | its own 30-minute cron |

Both run `main`. Differences live in each node's `.env`, which the deploy decrypts from a private
repo. Details: [docs/nodes.md](nodes.md).

| Door | Who gets in |
| :--- | :--- |
| Tailnet (Tailscale + Pi-hole + Caddy) | your devices; most apps ask nothing more |
| Public, behind Cloudflare Access | anyone who passes Access's login, through an outbound tunnel |
| Public, no login | only the deploy webhook (HMAC-signed) and apps public by design |

Plex is never proxied: it is reached on its LAN address, over Tailscale subnet routes from outside.
Details: [docs/access.md](access.md).

## What runs

| Module | Services |
| :--- | :--- |
| `compose-core.yml` | Caddy, Homepage, Pi-hole, Speedtest-tracker, cloudflared (opt-in) |
| `compose-media.yml` | Plex, Jellyfin, Seerr, Bazarr, Maintainerr, Tautulli, watch-next, subtitle-links, audio-convert; AceStream: gluetun, aceserve, kubo, bifrost, farnsworth; sleipnir, muninn |
| `compose-arrs.yml` | Prowlarr, Radarr, Sonarr, qBittorrent, FlareSolverr, Unpackerr, autobrr, qbit-manage, tracker-control, cross-seed, torrent-drop |
| `compose-mon.yml` | Prometheus, Grafana, Pushgateway, node-exporter, cAdvisor, blackbox-exporter, pi-metrics, qBittorrent and Pi-hole exporters, tailscale-metrics, VictoriaLogs, Vector |

Every service has a profile of its own name plus a group, so a node picks exactly what it runs.
Images for the services in `services/` are built by GitHub Actions and pulled, not compiled on the
Pi. bifrost, sleipnir and muninn are apps in their own repos, pinned here.

## How a change deploys

1. A push to `main` reaches the Pi through a GitHub webhook and a Cloudflare tunnel; a cron every
   30 minutes on each node catches anything missed.
2. `scripts/deploy/apply.sh` decrypts `.env`, pulls both repos, recreates only the containers whose
   image changed, and pushes the config that lives in apps' databases back into them.
3. A change to a `services/` image deploys when CI commits its new pin, minutes after the source push.

Details: [docs/deploy.md](deploy.md).

## What gets deleted

Nothing is deleted before it is watched. A watched film goes two days later; its torrent goes when
its tracker is paid, never before. [docs/lifecycle.md](lifecycle.md) has the rules and every
number.

## Everyday commands

```bash
bash scripts/deploy/apply.sh                         # deploy now, instead of waiting
tail -f ~/rpi-homeserver/apply.log                   # what the last deploys did
docker logs -f <container>                           # one service's output
bash scripts/recovery/rebuild-service.sh <service>   # rebuild one service from scratch
bash scripts/recovery/recovery-status.sh             # after a rebuild: what still needs a human
bash scripts/ops/backup.sh                           # a backup now
```

Run compose by hand with `export COMPOSE_ENV_FILES=versions.env,.env` first, or image versions do
not resolve.
