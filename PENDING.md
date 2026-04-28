# Pending tasks

## Bitwarden Secrets Manager — replace `.env` secrets

Replace the `.env` file secrets with [Bitwarden Secrets Manager](https://bitwarden.com/products/secrets-manager/).

**Why:** secrets in `.env` are plaintext on disk and in the repo history.
Bitwarden SM provides a proper vault with audit log, access control, and rotation.

**How to implement:**
1. Create a Bitwarden SM project and add secrets (JELLYFIN_API_KEY, GF_SECURITY_ADMIN_PASSWORD, PIHOLE_PASSWORD, TAILSCALE_API_KEY, etc.)
2. Install the [Bitwarden Secrets Manager CLI (`bws`)](https://github.com/bitwarden/sdk/releases) on the Pi
3. Generate a machine account token and store it as the only secret in `.env` (just `BWS_ACCESS_TOKEN=...`)
4. Create a wrapper script (`load-secrets.sh`) that runs `bws secret list` and exports vars before calling `docker compose up`
5. Or use the [Docker secrets integration](https://docs.docker.com/compose/use-secrets/) with Bitwarden SM as the backend

**Compose reference:** commented-out approach using `bws run`:
```yaml
# To launch with Bitwarden SM instead of .env:
# bws run -- docker compose -f compose-mon.yml up -d
#
# bws run injects all secrets from the SM project as environment variables,
# so compose files can reference them the same way as today (${VAR_NAME}).
```

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
