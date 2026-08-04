# Deploy on push (GitHub webhook + Cloudflare Tunnel)

The cron in `scripts/crontab` polls git every 30 minutes. This adds a push-triggered deploy so a
commit lands on the Pi in seconds, without opening any port on the router.

```
GitHub push ──► https://deploy.<domain>/hooks/deploy
                        │  (Cloudflare edge)
                        ▼
                 cloudflared container  ──► 127.0.0.1:9000
                                                │  deploy-webhook.py (systemd, user raspi)
                                                │  verifies X-Hub-Signature-256
                                                ▼
                                        scripts/deploy_control.sh  (under flock)
```

The cron stays enabled: it restarts anything that died and catches pushes that arrived while the
Pi or the tunnel was down.

## Security

The public URL is reachable by anyone, so the receiver treats every request as hostile:

- **HMAC first.** Every request must carry `X-Hub-Signature-256`, an HMAC-SHA256 of the raw body
  keyed with `GITHUB_WEBHOOK_SECRET`, compared with `hmac.compare_digest`. No valid signature
  means 401 and the deploy script is never reached. Without the secret a request cannot be forged.
- Only `POST` on the exact hook path, bodies capped at 1 MiB, only `push` events on
  `refs/heads/main`, and only for a hardcoded repo allowlist. Nothing from the payload is ever
  passed to a shell.
- The receiver binds `127.0.0.1`, so the tunnel is the only way in. It runs as `raspi`, not root.
- Cloudflare Access does **not** work in front of a webhook: GitHub cannot do an interactive login
  and cannot send service-token headers. Use a WAF rule instead if you want a second layer
  (see below).

Optional extra layer: a Cloudflare WAF custom rule on the hostname that blocks anything outside
GitHub's webhook source ranges, published at `https://api.github.com/meta` under `hooks`. Those
ranges do change, so this needs occasional review.

## Setup

### 1. Cloudflare tunnel (dashboard, one time)

1. Zero Trust → Networks → Tunnels → **Create a tunnel** → Cloudflared → name it `rpi`.
2. Copy the token it shows (the long string after `--token`) into `.env`:
   `CLOUDFLARED_TUNNEL_TOKEN=...`
3. In **Public Hostnames**, add exactly one:
   - Subdomain `deploy`, your domain, path `hooks/deploy`
   - Service: `HTTP` → `127.0.0.1:9000`

   Only what you list here becomes public. Do not add a catch-all hostname.

### 2. Secret and receiver (on the Pi)

```bash
openssl rand -hex 32                       # put it in .env as GITHUB_WEBHOOK_SECRET
sudo cp services/deploy-webhook/deploy-webhook.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now deploy-webhook
systemctl status deploy-webhook            # should say "listening on 127.0.0.1:9000/hooks/deploy"
```

### 3. Start the tunnel

Add `tunnel` to `COMPOSE_PROFILES` in `.env` (or use `all`), then:

```bash
export COMPOSE_ENV_FILES=versions.env,.env
docker compose up -d cloudflared
```

### 4. GitHub webhook (per repo)

In `rpi-homeserver` **and** `rpi-services` → Settings → Webhooks → Add webhook:

| Field | Value |
| :--- | :--- |
| Payload URL | `https://deploy.<domain>/hooks/deploy` |
| Content type | `application/json` |
| Secret | the `GITHUB_WEBHOOK_SECRET` value |
| Events | Just the push event |

GitHub sends a `ping` on save; the receiver answers `pong`. Check *Recent Deliveries* for a 200.

## Changing the receiver

The receiver is a **host systemd service**, so a deploy updates its code but does not reload it.
After changing `deploy-webhook.py`, restart it by hand or the old process keeps serving:

```bash
sudo systemctl restart deploy-webhook
```

Nothing warns you if you forget: the webhook keeps working, just with the previous code.

## Troubleshooting

```bash
journalctl -u deploy-webhook -f                  # receiver: signature rejections, triggers
docker logs -f cloudflared                       # tunnel health
tail -f ~/rpi-homeserver/deploy_control.log      # the deploy itself
```

- `401 unauthorized` in the journal: the secret in `.env` and in the GitHub webhook differ.
  Restart the service after changing `.env`.
- Deliveries time out: tunnel down, or the public hostname points somewhere other than
  `127.0.0.1:9000`.
- `Another deploy is already running, skipping`: expected when several pushes land together.
  The lock is `.deploy.lock` in the project dir.
