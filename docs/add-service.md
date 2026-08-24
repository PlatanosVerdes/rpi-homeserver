# How to Add a New Service

This guide covers adding a new Docker service with HTTPS access via Tailscale.

Two repos are involved — pick the right one before starting:

| Service type | Repo |
| :--- | :--- |
| Generic infrastructure (media, monitoring, tools anyone would use) | `rpi-homeserver` |
| Personal services (bots, personal integrations, domain-specific apps) | `rpi-services` |

---

## Step 1 — Add the service to a compose file

**For rpi-homeserver**, pick the right module:
- `compose-core.yml` — infrastructure (reverse proxy, DNS, dashboard)
- `compose-media.yml` — media players, streaming
- `compose-arrs.yml` — download automation
- `compose-mon.yml` — monitoring, metrics

**For rpi-services**, add it directly to `docker-compose.yml`.

Versions never go inline and are never `latest`: they live in `versions.env`, which is committed,
so a rebuild cannot silently pull a different image. Add the line there first:

```bash
FILEBROWSER_VERSION=v2.32.0
```

```yaml
# Example entry
  filebrowser:
    image: filebrowser/filebrowser:${FILEBROWSER_VERSION}
    container_name: filebrowser
    restart: unless-stopped
    profiles: [all]
    environment:
      - PUID=${PUID}
      - PGID=${PGID}
      - TZ=${TZ}
    volumes:
      - ${APP_CONFIG_PATH}/filebrowser:/config
      - ${DATA_ROOT}:/srv
    networks:
      - media-network
    labels:
      prometheus.probe: "http://filebrowser:80/"
```

> Do NOT add `ports:` for services that go through Caddy — Caddy reaches them by container name over `media-network`. Only add `ports:` if you need direct host access.

The `prometheus.probe` label is the whole monitoring setup: Prometheus asks Docker for the
containers carrying it and probes that URL every 15s, so the service appears in the **Service
probes** dashboard and in the "Service not answering" alert without anyone editing
`config/prometheus/prometheus.yml`. It works the same from either repo.

Give it a URL that a healthy app answers and a dead one does not, on the container's own port:
`/health`, `/healthz` or `/` are all fine, and any status up to 404 counts as alive (the module in
`config/blackbox/blackbox.yml` says which). A service on the host network is probed through
`http://host.docker.internal:<port>/` instead, because it has no name on `media-network`.

Leave the label out only when the service serves no HTTP at all — a bot, a cron worker, an
exporter. Those are covered by their own scrape job or, failing that, by the container being
alive, which is the "Not probed" table on the same dashboard.

---

## Step 2 — Add an HTTPS route in Caddy

**For rpi-homeserver services:** edit `config/caddy/Caddyfile`.

**For rpi-services services:** edit `rpi-services/config/caddy/services.caddy` (or create a new `.caddy` file there — all `*.caddy` files are auto-imported).

```caddyfile
https://filebrowser.platanosverdes.com {
    import cf_tls
    reverse_proxy filebrowser:8080
}

# Optional: HTTP short name for LAN access
http://filebrowser {
    reverse_proxy filebrowser:8080
}
```

The port is the container's **internal** port, not a host port.

---

## Step 3 — DNS (automatic)

Pi-hole is the DNS server for the tailnet, so every subdomain has to resolve to the Pi's Tailscale
address. Nothing to do by hand: `scripts/sync/pihole-dns.sh` runs on every deploy, reads the
hostnames out of the Caddy config from step 2, and writes the missing records into Pi-hole.

It only ever touches records it derived from Caddy, so anything added by hand stays. To check what
it did, look for the `[pihole-dns]` line in `apply.log`:

```
pihole DNS: 1 added, 0 re-pointed, 19 unchanged, 4 unrelated entries left untouched
```

---

## Step 4 — Add to Homepage dashboard (optional)

Edit `rpi-homeserver/config/homepage/services.yaml`:

```yaml
- Management:
    - Filebrowser:
        icon: filebrowser.png
        href: https://filebrowser.platanosverdes.com
        server: my-docker
        container: filebrowser
```

Icon names: search [walkxcode/dashboard-icons](https://github.com/walkxcode/dashboard-icons/tree/main/png) — use filename without extension.

---

## Step 5 — Deploy

### Either repo
A push to `main` deploys within seconds via the GitHub webhook
(see [deploy-webhook.md](deploy-webhook.md)). One script handles both repos, so to
trigger it by hand:
```bash
bash ~/rpi-homeserver/scripts/deploy/apply.sh
```

### Reload Caddy only (if you only changed a Caddyfile):
```bash
docker exec caddy caddy reload --config /etc/caddy/Caddyfile
```

---

## Step 6 — Add secrets if needed

1. Add the variable to `.env` in the relevant repo
2. Add it to `.env.example` with a placeholder and a comment
3. Reference it in the compose file as `${MY_SECRET}`

---

## Checklist

- [ ] Service added to correct compose file with `networks: media-network`
- [ ] `prometheus.probe` label set, or the service genuinely serves no HTTP
- [ ] HTTPS route added (Caddyfile or rpi-services `*.caddy`)
- [ ] Version pinned in `versions.env`, image referenced as `${SERVICE_VERSION}`
- [ ] Secrets added to `.env` and `.env.example`
- [ ] Homepage entry added (optional)
- [ ] Deployed and tested
