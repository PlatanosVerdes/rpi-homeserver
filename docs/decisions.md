# Decisions

Ideas that were tried or weighed and turned down, with the reason, so they are not tried again the
same way. Open work is in [PENDING.md](../PENDING.md).

## Dependabot for image updates: Renovate instead

Dependabot only reads literal tags in compose files and Dockerfiles. Every image here is
`${VARIABLE}` from `versions.env`, so it would have seen the GitHub Actions and the `golang`
builders and none of the services. Renovate reads `versions.env` through a regex manager and
handles the linuxserver tag scheme ([updates.md](updates.md)).

## speedtest-tracker: replaced

It averaged the most CPU of any container while idle: a Laravel app in which every request boots
PHP, called every 15 s by Prometheus and blackbox and every minute by its own scheduler cron, for
four tests a day. `services/speedtest` runs the same Ookla CLI on the same schedule and serves the
last result as metrics, so results stay comparable. Every result is appended to
`appdata/speedtest/results.jsonl`, one line per test (~1 KB once the list of servers Ookla tried is
dropped), so the history outlives Prometheus's 15 days without a database. Its web UI went with it;
a manual test is a POST to `/run` ([private-trackers.md](private-trackers.md)).

## Bitwarden Secrets Manager for `.env`: dropped

Tried as the home for `.env`, and it did not fit the workflow. SOPS + age with a private repo is
what is used ([secrets.md](secrets.md)). The wrapper script and its guide were deleted on 2026-08-22;
they are in git history if this is revisited.

## Podman instead of Docker: not worth it

The migration touches most of the repo and buys something this server barely needs.

| Piece | Why it is not a drop-in |
| :--- | :--- |
| `docker-compose.yml` | The whole layout rests on `include:` across four modules; `podman-compose` support for it is unproven |
| Homepage, cAdvisor, Vector | All talk to `/var/run/docker.sock`. Podman's socket is API-compatible and lives elsewhere; cAdvisor is the doubtful one |
| `restart: unless-stopped` on every service | No daemon restarts anything; it becomes systemd units (quadlet) |
| `/etc/docker/daemon.json` | Ignored. Podman logs to journald, so the log cap moves to the journal |
| `PUID`/`PGID` on linuxserver images | Rootless maps UIDs into a subordinate range, which collides with file ownership on the data disk. Rootful avoids it and throws away the reason to switch |
| Host network, Caddy on 80/443 | Rootless cannot bind below 1024 without extra configuration |

What it would buy is no root daemon, if run rootless. That matters on a machine exposed to the
internet, and this one barely is: what is public goes through an outbound tunnel behind Cloudflare
Access ([access.md](access.md)). Revisit if a service is ever exposed directly, or if Docker's
licensing or direction becomes a problem.

## Hardware transcoding on the Pi (v4l2m2m): tried, reverted

Jellyfin transcodes with `libx264`, and a Pi 4 cannot software-encode 1080p sport in real time
(1.39x on synthetic 1080p25 with the machine idle, halved at 50 fps). Mapping `/dev/video10-12` and
enabling hardware encoding failed in the real transcode:

```
[h264_v4l2m2m] VIDIOC_STREAMON failed on output context
[vost#0:0/h264_v4l2m2m] Error encoding a frame: No such process
```

The encoder works in isolation (2.45x), so the fault is in the transcode. Untested suspects, in
order: `gpu_mem` too low for the codec firmware, several ffmpeg processes contending for the single
encoder at `/dev/video11`, and the `high` profile at level 42. The better fix is upstream: the source
is H.264, so find why Jellyfin will not direct play or remux it. Not transcoding beats transcoding
efficiently.

## A per-channel AceStream health metric: abandoned

Probing an AceStream channel is not an HTTP HEAD: the engine joins the swarm, which is the same as
playing it, and this Pi plays one channel at a time. A sweep competes with whoever is watching, and
on the watched channel takes the engine away from them (one such extra connection made Jellyfin's
codec probe fail and the TV fall back to a re-encode the Pi could not sustain). Channels also change
hash several times an hour, so a value goes stale before the next sweep. What exists instead is
passive: bifrost's `/api/status` reports how every real open went, and it tries the next source by
itself when one is dead.

## Buying DigitalCore's leech bonus with disk: rejected

Holding that tracker's torrents to grow its leech bonus was implemented, measured and turned off: of
a 27% bonus, 9% came free from library hardlinks and 18% was bought with 179 GB of separate copies,
on an account that did not need the ratio. The mechanism stays in tracker-control, unconfigured.
Arithmetic in [private-trackers.md](private-trackers.md#why-the-leech-bonus-is-not-bought-with-disk).

## Searching C411 through a SOCKS proxy: stopped

C411 used to search through NordVPN's SOCKS5 proxy, to route around the ISP's match-night
interception. Its service credentials expired in silence, and the failure read exactly like a banned
account. It searches direct now; the cost is the match-night outage, which `indexer-retry.py`
shortens ([private-trackers.md](private-trackers.md#c411)).

## Jackett: not used

Prowlarr's predecessor. Prowlarr is already here, and cross-seed and autobrr integrate with it
directly.
