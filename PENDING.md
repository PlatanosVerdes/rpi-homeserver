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

## Pi-hole monitoring in Grafana

Pi-hole is installed natively (not Docker). To add metrics to Grafana:
1. Uncomment `pihole-exporter` in [compose-mon.yml](compose-mon.yml)
2. Add `PIHOLE_PASSWORD` to `.env` (or Bitwarden SM)
3. Add scrape job to [config/prometheus/prometheus.yml](config/prometheus/prometheus.yml):
   ```yaml
   - job_name: 'pihole'
     static_configs:
       - targets: ['pihole-exporter:9617']
   ```
4. Import Grafana dashboard ID `10176` from grafana.com

---

## Tailscale monitoring in Grafana

To monitor which Tailscale devices are online:
1. Uncomment `tailscale-exporter` in [compose-mon.yml](compose-mon.yml)
2. Add `TAILSCALE_API_KEY` and `TAILSCALE_TAILNET` to `.env`
3. Add scrape job to Prometheus config
4. Import Grafana dashboard ID `17298` from grafana.com
