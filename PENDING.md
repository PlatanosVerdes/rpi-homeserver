# Pending tasks

Open work only. Ideas turned down, with the reason, are in [docs/decisions.md](docs/decisions.md).

## Alert when a pinned version has a safe update

**Goal:** a Grafana alert that fires only when a `versions.env` pin is behind upstream **and** no
release in between mentions a breaking change, so it never nags about the ones that need reading.

**Why not done:** "safe" takes several sources to get right:
- LinuxServer images: use `https://api.linuxserver.io/api/v1/images` (field `version`); their raw
  tag lists sort wrong (`linuxserver/radarr` has an ancient tag `5.14`). `lscr.io` authenticates
  through `ghcr.io`'s token realm.
- GHCR's `tags/list` paginates; ask with `?n=2000` and do not trust one unpaginated call.
- Anything with GitHub releases: `gh api repos/<owner>/<repo>/releases`, the only place with
  changelog text to grep for "breaking". `gh` is installed and authenticated on the Pi.
- Images pinned by digest (aceserve, qbittorrent-exporter, pihole6_exporter) have no version to
  compare.

**Next step:** a daily script that walks every release between pin and latest, pushes
`service_update_available{name,current,latest,breaking}` to Pushgateway, and one rule on
`breaking="false"`. Read every note in a multi-version jump, not only the latest; Grafana 12→13 and
Caddy v2.11.4 were real cases that needed a human.

## "Docker monitoring" lives only in Grafana's database

Imported by hand with corrected queries (`container_cpu_usage_seconds_total`,
`container_memory_working_set_bytes`), because file-provisioning it failed: its UID had existed
before, and Grafana v13 silently ignores a provisioned dashboard whose UID was deleted
([alerting.md](docs/alerting.md#provisioning-traps)). If it is lost, re-import it and fix the same two
metrics; provisioning it needs a fresh UID and the cause understood.

## Cast to the home TV from outside the house

**Goal:** start playback on the TV from a phone away from home. **Blocker:** discovery. Chromecast
and Plex Companion use mDNS, and Tailscale carries no multicast, so a wider subnet route alone does
not help. **Next step, if wanted:** a hub on the LAN that does its own discovery and is commanded over
the tailnet, e.g. Home Assistant's Google Cast integration (`media_player.play_media`). Nothing of
it exists yet.

## Retire `scripts/ops/cutoff-search.sh`

Out of the crontab since 2026-08-24, script kept. It covered releases that scrolled out of the RSS
window. Decide whether anything replaces it, write the reason here, then delete the script and its
commented cron line together.

## Smaller items

- **The orphan `Socks5` proxy in Prowlarr** (`nl.socks.nordhold.net:1080`, tag `nordvpn`) has no
  indexer tagged to it. Delete it, or it is a trap for whoever tags something `nordvpn` next.
- **C411's own figures.** `/api/auth/me` returns `uploadCredit` and `minRatioForDownload`; tracker-control
  reads neither, and the 0.8 in `config/trackers/rules.json` is typed in by hand.
- **A DHCP reservation for the Pi's eth0.** The `/32` routes Tailscale advertises are typed in by
  hand; wifi is reserved, cable is a plain lease. If it moves, Plex remote breaks silently.
- **`config/qbittorrent/seed-rules.json` is read by nothing**; seed goals are in
  `config/qbit-manage/config.yml`. Delete it, and the notes in `config/trackers/rules.json` that
  still quote its 360 h / 240 h.
- **`config/arr-exports/` is a July snapshot** that differs from `config/arr/`, which is what
  `scripts/sync/arr-config.sh` pushes. Delete the directory.
- **The "Acestream updater not running" alert** reads `acestream_last_run_timestamp`, from the
  updater bifrost replaced. Check whether anything still writes it; fix or delete the rule.
- **Stale comments in code**: the commented `*/15` cron example at the end of `apply.sh` (and its
  "15-min run" prune comment), and the Apple 2FA step in `recovery-status.sh`'s header, which the
  script does not check.
