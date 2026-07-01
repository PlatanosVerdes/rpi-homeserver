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
