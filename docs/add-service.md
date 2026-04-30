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

```yaml
# Example entry
  filebrowser:
    image: filebrowser/filebrowser:latest
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
```

> Do NOT add `ports:` for services that go through Caddy — Caddy reaches them by container name over `media-network`. Only add `ports:` if you need direct host access.

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

## Step 3 — Add a DNS record in Pi-hole

Pi-hole is the DNS server for the Tailscale network. Every subdomain must resolve to the Pi's Tailscale IP.

1. Open Pi-hole: `https://pihole.platanosverdes.com` → Settings → Local DNS → DNS Records
2. Add:
   - Domain: `filebrowser.platanosverdes.com`
   - IP: `TAILSCALE_IP` (e.g. `100.322.71.20`)

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

### rpi-homeserver
```bash
# Push to git and wait up to 15 min, or trigger manually:
bash ~/rpi-homeserver/scripts/deploy_control.sh
```

### rpi-services
```bash
# Push to git and wait up to 15 min, or trigger manually:
bash ~/rpi-services/scripts/deploy_control.sh
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
- [ ] HTTPS route added (Caddyfile or rpi-services `*.caddy`)
- [ ] DNS record added in Pi-hole → Local DNS
- [ ] Secrets added to `.env` and `.env.example`
- [ ] Homepage entry added (optional)
- [ ] Deployed and tested
