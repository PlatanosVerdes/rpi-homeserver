# Logging

Prometheus answers "how much" and "is it up". It cannot answer "what did this container say at
03:14", which is what you need when something broke an hour ago and the container has since
restarted. This adds a searchable log store next to it, on the same Grafana.

```
containers ──► vector (docker socket, ro) ──► victorialogs ──► Grafana
                                                   │
                                              /mnt/data/db
```

| Piece | What it does | Footprint |
| :--- | :--- | :--- |
| `victorialogs` | Stores and queries logs, 30 day retention | ~25 MB RAM |
| `vector` | Follows every container's stdout/stderr and ships it | ~52 MB RAM |

Use VictoriaLogs rather than Grafana Loki here. On a Raspberry Pi, Loki is the component you
notice, and what it adds over VictoriaLogs (multi-tenancy, object storage) is worth nothing on a
single box.

## How to read the logs

- **Grafana** → Explore → **VictoriaLogs** datasource. Provisioned from
  `config/grafana/provisioning/datasources/victorialogs.yml`, using the
  `victoriametrics-logs-datasource` plugin installed via `GF_INSTALL_PLUGINS` on the Grafana service.
- **Its own UI** at `https://logs.platanosverdes.com/select/vmui/`, which is better for ad-hoc
  digging than Explore.

The query language is LogsQL, not PromQL and not LogQL:

```logsql
_time:1h caddy                          # everything from the caddy container in the last hour
_time:24h container:plex error          # errors from Plex in the last day
_time:5m | stats by (container) count() # who is noisy right now
```

## What is collected, and what is not

Every container's stdout/stderr, tagged with `container` and `stream`. Nothing else: not the host's
journal, not `auth.log`, and not Caddy access logs, which Caddy does not write unless a site is
given a `log` directive.

**Applications that log to a file rather than stdout will not appear here at all.** Plex and
qBittorrent are the two that matter on this box: both keep their own log files inside their config
directory, so `container:plex` stays empty no matter what Plex is doing. Read those with
`docker exec`, or point Vector at the file if it is ever worth the mount.

Stream fields are deliberately just `container` and `stream`. VictoriaLogs indexes by stream, so
adding something high-cardinality like the image tag would create a new stream on every version
bump and blow up the index.

## Storage

The database lives on `${DATA_DB_ROOT}/victorialogs`, i.e. the data disk, **never the SD card**: a
log store writes constantly and would wear the card out. Retention is 30 days
(`-retentionPeriod=30d` in `compose-mon.yml`). Check what it is actually using:

```bash
sudo du -sh /mnt/data/db/victorialogs
```

## The Docker socket

Vector mounts `/var/run/docker.sock` read-only to enumerate containers and follow their logs. That
is root-equivalent access to the host, so it is worth stating plainly: it grants nothing new here,
because cAdvisor and Homepage already mount it. The `json-file` driver is untouched, so
`docker logs <name>` keeps working exactly as before.

## Removing it

Nothing else depends on this, so it comes out cleanly:

```bash
docker compose stop victorialogs vector && docker compose rm -f victorialogs vector
sudo rm -rf /mnt/data/db/victorialogs
```

Then drop the two services from `compose-mon.yml`, their two lines from `versions.env`,
`config/vector/`, `config/grafana/provisioning/datasources/victorialogs.yml`, the
`GF_INSTALL_PLUGINS` line on Grafana, and the two `logs` routes from the Caddyfile.

## Troubleshooting

- **No logs at all.** `docker logs vector` shows what it is watching. It captures "from now on", so
  it never backfills anything written before it started.
- **A container is missing.** Vector excludes itself by name to avoid a feedback loop
  (`exclude_containers` in `config/vector/vector.yaml`); nothing else is filtered.
- **Grafana says the datasource type is unknown.** The plugin is downloaded at container start, so
  Grafana needs working DNS and internet on boot. `docker exec grafana ls /var/lib/grafana/plugins/`
  should list `victoriametrics-logs-datasource`.
- **The disk is filling.** Lower `-retentionPeriod` and restart the container.

## References

- [VictoriaLogs docs](https://docs.victoriametrics.com/victorialogs/)
- [LogsQL reference](https://docs.victoriametrics.com/victorialogs/logsql/)
- [Vector docker_logs source](https://vector.dev/docs/reference/configuration/sources/docker_logs/)
