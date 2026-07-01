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

## Tailscale monitoring in Grafana — needs API key

Tailnet name: **Bannet**. Service is ready in [compose-mon.yml](compose-mon.yml), just needs activation:

1. Generate a Tailscale **API key** (not auth key):
   → https://login.tailscale.com/admin/settings/keys → "Generate API access token"
2. Add to `.env`:
   ```
   TAILSCALE_API_KEY=tskey-api-xxxxx
   ```
3. Uncomment `tailscale-exporter` in [compose-mon.yml](compose-mon.yml)
4. Uncomment `tailscale` scrape job in [config/prometheus/prometheus.yml](config/prometheus/prometheus.yml)
5. Start: `docker compose -f compose-mon.yml up -d tailscale-exporter`
6. Import Grafana dashboard ID `17722` from grafana.com

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
