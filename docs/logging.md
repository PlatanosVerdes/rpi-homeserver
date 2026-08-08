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

Prometheus tells you the disk filled at 3am. VictoriaLogs tells you what was writing to it.

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

Two doors to the same data:

- **Grafana** → Explore → pick the **VictoriaLogs** datasource. Best when you already have a
  dashboard open.
- **Its own web UI** at `https://logs.platanosverdes.com/select/vmui/`. Better for digging around,
  because it shows the fields as they really are.

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
  Use `journalctl` on the Pi for those.
- **Caddy's access log.** Caddy records errors, but not every request, unless a site is given a
  `log` directive. Nothing here turns that on.

## Where it is stored

On the external data disk, at `${DATA_DB_ROOT}/victorialogs`, and **never on the SD card**. A log
store writes constantly, and constant writes are what kill SD cards. Retention is 30 days, set by
`-retentionPeriod=30d` in `compose-mon.yml`.

```bash
sudo du -sh /mnt/data/db/victorialogs     # how much it is actually using
```

## About the Docker socket

Vector mounts `/var/run/docker.sock` read-only. That socket is how you talk to the Docker daemon,
and access to it is effectively root on the host, so it deserves a sentence rather than silence:
Vector uses it only to list containers and follow their output, and cAdvisor and Homepage on this
same server already mount it, so it grants nothing that was not already granted. The `json-file`
driver is untouched, so `docker logs <name>` still behaves exactly as before.

## If something looks wrong

- **No logs at all.** `docker logs vector` shows what it decided to watch. It only captures from
  the moment it starts, so it never backfills anything written before that.
- **One container is missing.** First check it is actually printing anything:
  `docker logs --since 15m <name>`. Most of the quiet ones are quiet, not broken. Vector filters
  only itself, to avoid it reporting on its own reporting.
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
