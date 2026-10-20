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

### A service whose code lives in `services/`

It still gets a compose entry like the one above, with `build:` pointing at its directory **and**
an `image:` line, and its tag goes in `versions.env` as a value the workflow did not write:

```bash
# versions.env, under "built here"
MYTOOL_IMAGE_TAG=unbuilt
```

```yaml
  mytool:
    image: ghcr.io/platanosverdes/rpi-homeserver-mytool:${MYTOOL_IMAGE_TAG}
    build:
      context: ./services/mytool
      args:
        ALPINE_VERSION: ${MYTOOL_ALPINE_VERSION}   # base image versions, also in versions.env
```

Nothing else to register. On the merge, the **Service images** workflow sees `unbuilt`, builds the
image on an arm64 runner, publishes it to GHCR and commits the real tag, and that commit is what the
Pi deploys: it pulls the image instead of compiling it. Every later change under the directory, or
to one of its build args in `versions.env`, does the same. Its pull request builds it too, without
publishing anything.

Leave the `image:` line out and the workflow only checks that it builds; the Pi then builds it
itself on every change, as everything here used to. Add a `.dockerignore` that lets through only
what the Dockerfile copies. The whole mechanism, and what it does not do:
[service-images.md](service-images.md).

> Do NOT add `ports:` for services that go through Caddy — Caddy reaches them by container name over `media-network`. Only add `ports:` if you need direct host access.

When something on the host does need it, such as a script in `scripts/` calling
`http://localhost:<port>`, publish on the loopback address only (`"127.0.0.1:7878:7878"`): the
script works and the port is not open to the LAN or the tailnet. A service on the host network
publishes nothing, so it declares what it binds in a `host.ports` label instead
(`host.ports: "8096/tcp 7359/udp"`, or `"none"`).

The **Host ports** workflow runs `scripts/ci/ports.py` on every PR, against this repo and
rpi-services together since both run on the same Pi. It fails when two containers claim the same
host port, or when a host-network service has no `host.ports` label, and its job summary lists
every port in use. Run it locally with `python3 scripts/ci/ports.py . ../rpi-services`.

The `prometheus.probe` label is the whole monitoring setup: Prometheus asks Docker for the
containers carrying it and probes that URL every 15s, so the service appears in the **Service
probes** dashboard and in the "Service not answering" alert without anyone editing
`config/prometheus/prometheus.yml`. It works the same from either repo.

Give it a URL that a healthy app answers and a dead one does not, on the container's own port:
`/health`, `/healthz` or `/` are all fine, and any status up to 404 counts as alive (the module in
`config/blackbox/blackbox.yml` says which). A service on the host network is probed through
`http://host.docker.internal:<port>/` instead, because it has no name on `media-network`.

A service published through Cloudflare Access can add a second label, `prometheus.probe.access`,
with its **public** URL (`https://app.example.com/`). That one is probed from the internet's side,
through public DNS rather than Pi-hole, and is healthy only when Access answers a stranger with the
redirect to its login, so it catches a broken tunnel route, DNS record or Access application, and a
route left open without a login. Its alert, "Public route behind Access broken", stays quiet while
every public URL is failing at once, which is a blocked Cloudflare range or a dead tunnel rather
than this app.

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

The deploy reloads Caddy on its own when a push changes `config/caddy/` in either repo, so a new
route in rpi-services goes live with the push like one here.

### Reload Caddy by hand (an edit on the Pi that is not pushed yet):
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
- [ ] No `ports:` unless the host needs it (then `127.0.0.1:`), and `host.ports` if on the host network
- [ ] HTTPS route added (Caddyfile or rpi-services `*.caddy`)
- [ ] Version pinned in `versions.env`, image referenced as `${SERVICE_VERSION}`
- [ ] Built from `services/`: `image: ghcr.io/platanosverdes/rpi-homeserver-<name>:${<NAME>_IMAGE_TAG}`
      beside `build:`, `<NAME>_IMAGE_TAG=unbuilt` in `versions.env`, a `.dockerignore`
- [ ] Secrets added to `.env` and `.env.example`
- [ ] Homepage entry added (optional)
- [ ] Deployed and tested
