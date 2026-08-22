# Logging

## What this is for

Every container on this server writes text as it works: what it did, what it failed to do, what it
is waiting for. That text is called a **log**. Docker keeps a bit of it and throws the rest away,
which is fine until the day something breaks and the evidence is already gone.

This adds two pieces so that text is kept for 30 days and can be searched from one place.

It is not the same job as Prometheus, which is already on this server and is easy to confuse with
this:

| | Prometheus | VictoriaLogs |
| :--- | :--- | :--- |
| Stores | **Numbers** over time: CPU 42%, disk 35%, "is it alive: yes" | **Text**: `connection refused`, `attest failed` |
| Answers | *how much*, *is it up* | *what did it say, and why* |
| Good for | Dashboards and alerts | Working out what happened after the fact |

Prometheus reports that the disk filled at 3am. VictoriaLogs holds the lines showing what was
writing to it.

## How it fits together

```
each container prints text
        │
        ▼
   vector          reads the text as it appears and forwards it
        │
        ▼
  victorialogs     stores it for 30 days and answers searches
        │
        ▼
    Grafana        where you read it
```

| Piece | Its one job | Footprint |
| :--- | :--- | :--- |
| `vector` | Watches every container and forwards whatever they print | ~52 MB RAM |
| `victorialogs` | Keeps 30 days of it, answers queries | ~25 MB RAM |

Use VictoriaLogs rather than the better-known Grafana Loki. On a Raspberry Pi, Loki is the
component you notice; what it adds (multi-tenancy, object storage) is worth nothing on one box.

## Reading the logs

Two interfaces onto the same data:

- **Grafana** → Explore → **VictoriaLogs** datasource.
- **VictoriaLogs' own web UI** at `https://logs.platanosverdes.com/select/vmui/`, which shows the
  raw fields and is the better one for exploratory queries.

Queries use a language called **LogsQL**. It is not PromQL (Prometheus) and not LogQL (Loki), so
examples found online for those will not work here. Start from these:

```logsql
_time:1h caddy                            # anything from the caddy container, last hour
_time:24h container:plex error            # lines containing "error" from plex, last day
_time:5m | stats by (container) count()   # who is talking right now
```

The parts, in plain terms:

| Piece | What it means |
| :--- | :--- |
| `_time:1h` | How far back to look. Almost always the first thing you write |
| `caddy` | A bare word is a full-text search across the line |
| `container:plex` | Match a specific field instead of the whole line |
| `\| stats by (x) count()` | Group and count, like a `GROUP BY` |

Two fields exist on every line: `container` (which one printed it) and `stream` (`stdout` or
`stderr`). The line itself is `_msg`.

Those two are the only fields VictoriaLogs indexes by, and that is deliberate. It organises data
into **streams**, one per distinct combination of those fields, and a field with many possible
values (an image tag, a request id) would create a new stream every time it changed and make the
index enormous. Keep new stream fields few and boring.

## What is not collected

Worth knowing before you go looking for something that was never there:

- **Applications that write to a file instead of the screen.** Plex and qBittorrent both keep their
  own log files inside their config directory, so `container:plex` stays empty no matter what Plex
  is doing. Read those with `docker exec`, or point Vector at the file if it earns the mount.
- **The host itself.** The Raspberry Pi's own system log, SSH logins and `auth.log` are not here.
  Use `journalctl` on the Pi for those. The repo's own `*.log` files are, though — see below.
- **Requests that never reach Caddy.** Plex is deliberately not proxied and Jellyfin apps often talk
  to the Pi directly by IP, so neither shows up in the access log below. It records what went
  through the reverse proxy, which is not the same as everything you watched.

## The cron scripts' logs

`scripts/deploy/apply.sh`, `scripts/ops/backup.sh`, `scripts/ops/cutoff-search.sh` and friends run from cron and append to files in the
repo root rather than printing to a container, so the `docker_logs` source cannot see them. A
`file` source collects those too, which is how the deploy and backup history got in here at all.

They are keyed by file name, so the same field works for both kinds:

```logsql
container:apply AND "Caddy config changed"    # deploys that reloaded Caddy
container:backup                              # nightly backup history
stream:file                                   # everything that came from a file rather than a container
```

The companion repo's logs come too, `one-pace.log` among them, through `EXT_LOGS_PATH` in `.env` —
the same mechanism that brings its Caddy routes in through `EXT_CADDY_PATH`. This repo never learns
where the other one lives, and mounts an empty directory when there is no companion.

One thing worth knowing: Vector reads each repo root because the `.log` files sit loose in them and
a bind mount cannot take a glob, which means the container can see each `.env`. That is a smaller
exposure than the docker socket it already mounts, but worth stating rather than leaving to be
discovered. And since the stream name is the file's basename, two repos must not both own a log
with the same name or they would merge into one stream.

## The Caddy access log

Every site block in the Caddyfile carries a `log` directive, so Caddy writes one JSON line per
request to stderr, where Vector picks it up like any other container output. There is no extra
pipeline and no file on disk to rotate.

It exists because nothing else knows which services actually get *used*. Prometheus measures whether
they are up; being up and being opened are different questions, and only this answers the second.

The blackbox probes hit containers directly (`http://jellyfin:8096/health`), not these routes, so
the access log is real browser traffic rather than a health check every 30 seconds.

Which hosts you opened, busiest first:

```logsql
container:caddy AND _msg:"handled request" | stats by (request.host) count() as hits | sort by (hits desc)
```

Bear in mind one page view is many lines: a web UI pulls scripts, icons and API calls, all of them
logged. It ranks reliably but the counts are requests, not visits.

## Where it is stored

On the external data disk, at `${DATA_DB_ROOT}/victorialogs`, and **never on the SD card**. A log
store writes constantly, and constant writes are what kill SD cards. Retention is 30 days, set by
`-retentionPeriod=30d` in `compose-mon.yml`.

```bash
sudo du -sh /mnt/data/db/victorialogs     # how much it is actually using
```

## About the Docker socket

Vector mounts `/var/run/docker.sock` read-only. Access to that socket is effectively root on the
host, so it is worth stating explicitly: Vector uses it only to list containers and follow their
output, and cAdvisor and Homepage on this same server already mount it, so it grants nothing that
was not already granted. The `json-file` driver is untouched, so `docker logs <name>` behaves as
before.

## If something looks wrong

- **No logs at all.** `docker logs vector` shows what it decided to watch. It only captures from
  the moment it starts, so it never backfills anything written before that.
- **One container is missing.** First check it is actually printing anything:
  `docker logs --since 15m <name>`. Several containers here are simply silent. The only exclusion
  Vector applies is itself, which would otherwise feed its own output back in.
- **`Error in communication with Docker daemon ... container which is dead or marked for removal`.**
  Vector was following a container while a deploy replaced it. It reattaches on its own.
- **Grafana says the datasource type is unknown.** The plugin downloads when Grafana starts, so
  Grafana needs DNS and internet at boot. Check with
  `docker exec grafana ls /var/lib/grafana/plugins/`.
- **The disk is filling.** Lower `-retentionPeriod` and restart the container.

## Removing it

Nothing else depends on this, so it comes out cleanly:

```bash
docker compose stop victorialogs vector && docker compose rm -f victorialogs vector
sudo rm -rf /mnt/data/db/victorialogs
```

Then drop the two services from `compose-mon.yml`, their two lines from `versions.env`,
`config/vector/`, `config/grafana/provisioning/datasources/victorialogs.yml`, the
`GF_INSTALL_PLUGINS` line on Grafana, and the two `logs` routes from the Caddyfile.

## References

- [VictoriaLogs docs](https://docs.victoriametrics.com/victorialogs/)
- [LogsQL reference](https://docs.victoriametrics.com/victorialogs/logsql/)
- [Vector docker_logs source](https://vector.dev/docs/reference/configuration/sources/docker_logs/)
