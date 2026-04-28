# Secrets Manager — Bitwarden SM

All sensitive credentials are stored in [Bitwarden Secrets Manager](https://bitwarden.com/products/secrets-manager/) (BWS). Only `BWS_ACCESS_TOKEN` stays in `.env`.

## How it works

```
.env  (non-secret config + BWS_ACCESS_TOKEN)
        │
        ▼
scripts/bws-run.py          ← fetches ALL secrets in one API call
        │  plain secret  → VAR_NAME=value
        │  JSON secret   → each key becomes a separate VAR_NAME=value
        ▼
docker compose up -d        ← receives secrets as env vars, never touches disk
        ▼
containers
```

`deploy_control.sh` runs this automatically every 15 minutes on git pull.

---

## Secret structure in BWS

| BWS secret | Variables inside | Notes |
|---|---|---|
| `ARR` | `PROWLARR_API_KEY`, `RADARR_API_KEY`, `SONARR_API_KEY` | JSON |
| `MEDIA` | `PLEX_API_TOKEN`, `PLEX_CLAIM`, `OVERSEERR_API_KEY`, `JELLYFIN_API_KEY`, `QBIT_PASSWORD` | JSON |
| `MONITORING` | `PIHOLE_PASSWORD`, `SPEEDTEST_KEY`, `SPEEDTEST_API_KEY` | JSON |
| `VPN` | `NORDVPN_USER`, `NORDVPN_PASS` | JSON |
| `INFRA` | `CF_API_TOKEN`, `VAULTWARDEN_ADMIN_TOKEN` | JSON |
| `BOT_POL_ACADEMY_OFFERS_TOKEN` | — | Plain string |
| `TAILSCALE_API_KEY` | — | Plain string — read by tailscale-metrics binary directly from `.env`, not via BWS |

---

## What stays in `.env`

```bash
# Non-secret config
TZ=Europe/Madrid
STATIC_IP=192.168.1.180
TAILSCALE_IP=100.125.71.20
PUID=1000 / PGID=1000
DATA_ROOT, DATA_DB_ROOT, CONFIG_ROOT, APP_CONFIG_PATH
QBIT_WEBUI_PORT=8080
VAULTWARDEN_DOMAIN=https://vault.platanosverdes.com

# Read directly by tailscale-metrics Go binary (not injected via BWS)
TAILSCALE_API_KEY=tskey-api-...

# The only real credential that stays here
BWS_ACCESS_TOKEN=0.xxx...
```

---

## Running docker compose with secrets

Always use `bws-run.py` when starting or restarting services. **Never use plain `docker compose up`** — it won't have the secrets.

```bash
# 1. Load BWS_ACCESS_TOKEN into your shell
export $(grep BWS_ACCESS_TOKEN .env | tr -d '"' | xargs)

# 2. Run any compose command — works exactly like docker compose
python3 scripts/bws-run.py docker compose -f compose-core.yml up -d
python3 scripts/bws-run.py docker compose -f compose-mon.yml up -d
python3 scripts/bws-run.py docker compose -f compose-core.yml restart homepage
python3 scripts/bws-run.py docker compose -f compose-core.yml up -d caddy
```

The wrapper makes **one API call** to BWS, injects all secrets into the process environment, then hands off to docker compose. Secrets are never written to disk.

### Restart a single service after a secret changes

```bash
export $(grep BWS_ACCESS_TOKEN .env | tr -d '"' | xargs)
python3 scripts/bws-run.py docker compose -f compose-core.yml up -d <service-name>
```

### Debug: see all injected env vars

```bash
export $(grep BWS_ACCESS_TOKEN .env | tr -d '"' | xargs)
python3 scripts/bws-run.py printenv | sort
```

### Debug: see resolved compose config (all ${VAR} substituted)

```bash
export $(grep BWS_ACCESS_TOKEN .env | tr -d '"' | xargs)
python3 scripts/bws-run.py docker compose -f compose-core.yml config
```

---

## Automated deploys

`deploy_control.sh` runs every 15 minutes via cron. It:
1. Loads `BWS_ACCESS_TOKEN` from `.env`
2. Checks BWS is reachable
3. Runs `python3 scripts/bws-run.py docker compose up -d` on git changes
4. Reports `deploy_bws_secrets_ok` metric to Grafana (visible in Deploy Monitor dashboard)

If BWS is unreachable, it falls back to plain `docker compose` (no secrets injected — containers may fail to start if they need secrets).

---

## Adding a new secret

1. Go to [vault.bitwarden.com](https://vault.bitwarden.com/#/sm) → **Secrets Manager** → HomeLab project
2. Add to an existing group (edit the JSON value) or create a new plain secret
3. No code changes needed — next `bws-run.py` call picks it up automatically

**Example — add a new key to an existing group:**
Edit the `MEDIA` secret and add `"NEW_VAR": "value"` to the JSON object.

**Example — add a standalone secret:**
Create secret with key `MY_NEW_TOKEN` and value `abc123`.

---

## Crons that use secrets

| Cron | Needs BWS? | Notes |
|---|---|---|
| `deploy_control.sh` (every 15 min) | ✅ Yes | Uses `bws-run.py` automatically |
| `tailscale-metrics` (every 1 min) | ❌ No | Reads `TAILSCALE_API_KEY` from `.env` directly |
