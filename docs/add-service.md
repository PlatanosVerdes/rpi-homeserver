# How to Add a New Service

This guide walks through adding a new Docker service with HTTPS access via Tailscale.

---

## Step 1 — Add the service to a compose file

Pick the right module:
- `compose-core.yml` — infrastructure, networking tools
- `compose-media.yml` — media players, streaming
- `compose-arrs.yml` — download automation
- `compose-mon.yml` — monitoring, metrics
- `compose-apps.yml` — custom apps, bots

```yaml
# Example: adding Filebrowser
  filebrowser:
    image: filebrowser/filebrowser:latest
    container_name: filebrowser
    restart: unless-stopped
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

> Do NOT add a `ports:` mapping for services that will go through Caddy — Caddy reaches them by container name over `media-network`. Only add `ports:` if you need direct host access.

---

## Step 2 — Add an HTTPS route in Caddyfile

Open `config/caddy/Caddyfile` and add two blocks: one for HTTPS (Tailscale/remote) and one for HTTP (LAN short name).

```caddyfile
# At the end of the HTTPS section
https://filebrowser.platanosverdes.com {
    import cf_tls
    reverse_proxy filebrowser:8080
}

# At the end of the HTTP section
http://filebrowser {
    reverse_proxy filebrowser:8080
}
```

The port (`8080`) is the container's **internal** port, not a host port.

---

## Step 3 — Add a DNS record in Pi-hole

Pi-hole is the DNS server for the Tailscale network. Every subdomain must resolve to the Pi's Tailscale IP.

1. Open Pi-hole admin: `http://pihole/admin` (or `https://pihole.platanosverdes.com`)
2. Go to **Local DNS → DNS Records**
3. Add:
   - Domain: `filebrowser.platanosverdes.com`
   - IP: your `TAILSCALE_IP` (e.g. `100.125.71.20`)

This makes the subdomain reachable from any Tailscale device.

---

## Step 4 — Update `/etc/hosts` on client devices (LAN access)

For the HTTP short name to work on your laptop/desktop, add the new name to the existing line:

```
# Find and edit this line in /etc/hosts
192.168.1.xxx  raspi homepage jellyfin ... filebrowser
```

> You only need this for the HTTP short name. The HTTPS subdomain works automatically via Pi-hole DNS + Tailscale.

---

## Step 5 — Add to Homepage dashboard (optional)

Edit `config/homepage/services.yaml` and add an entry under the appropriate group:

```yaml
- Management:
    - Filebrowser:
        icon: filebrowser.png
        href: https://filebrowser.platanosverdes.com
        server: my-docker
        container: filebrowser
```

Homepage icons: search at [walkxcode/dashboard-icons](https://github.com/walkxcode/dashboard-icons/tree/main/png) — use the filename without extension.

---

## Step 6 — Deploy

Push to git and wait for the cron (up to 15 min), or trigger manually:

```bash
bash scripts/deploy_control.sh
```

Caddy picks up the new Caddyfile automatically on container restart. If Caddy is already running and you only changed the Caddyfile, reload it:

```bash
docker exec caddy caddy reload --config /etc/caddy/Caddyfile
```

---

## If the service needs a secret

1. Add the variable to `.env` (current state — BWS migration pending)
2. Reference it in the compose file as `${MY_NEW_SECRET}`
3. When BWS migration is done: add it to the appropriate secret group in Bitwarden SM (see [docs/secrets-manager.md](secrets-manager.md))

---

## Checklist

- [ ] Service added to a `compose-*.yml` with correct network
- [ ] HTTPS block added to `config/caddy/Caddyfile`
- [ ] HTTP short name block added to `config/caddy/Caddyfile`
- [ ] DNS record added in Pi-hole → Local DNS
- [ ] Homepage entry added
- [ ] `/etc/hosts` updated on client devices (for LAN short name) (OPTIONAL)
- [ ] Deployed and tested via both HTTPS and HTTP