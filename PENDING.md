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
