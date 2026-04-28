# Secrets Manager — Bitwarden SM

All sensitive credentials are stored in [Bitwarden Secrets Manager](https://bitwarden.com/products/secrets-manager/) (BWS), not in `.env`.

## How it works

```
.env (BWS_ACCESS_TOKEN only)
        ↓
scripts/bws-run.py
        ↓  fetches all secrets (one API call)
        ↓  expands JSON-valued secrets into individual env vars
        ↓
docker compose (receives secrets as environment variables)
        ↓
containers
```

`deploy_control.sh` runs `bws-run.py docker compose up -d` automatically on every deploy. Secrets are injected in memory — never written to disk.

## Secret structure in BWS

| BWS secret name | Variables inside | Type |
|---|---|---|
| `ARR` | `PROWLARR_API_KEY`, `RADARR_API_KEY`, `SONARR_API_KEY` | JSON object |
| `MEDIA` | `PLEX_API_TOKEN`, `PLEX_CLAIM`, `OVERSEERR_API_KEY`, `JELLYFIN_API_KEY`, `QBIT_PASSWORD` | JSON object |
| `MONITORING` | `PIHOLE_PASSWORD`, `SPEEDTEST_KEY`, `SPEEDTEST_API_KEY` | JSON object |
| `VPN` | `NORDVPN_USER`, `NORDVPN_PASS` | JSON object |
| `INFRA` | `CF_API_TOKEN`, `VAULTWARDEN_ADMIN_TOKEN` | JSON object |
| `BOT_POL_ACADEMY_OFFERS_TOKEN` | — | Plain string |
| `TAILSCALE_API_KEY` | — | Plain string (read by tailscale-metrics binary, not via BWS) |

Secrets with a JSON object value are automatically expanded: the key names inside the JSON become the environment variable names.

## What stays in `.env`

Only non-sensitive configuration and the BWS access token:

```bash
TZ, STATIC_IP, TAILSCALE_IP, PUID, PGID
DATA_ROOT, DATA_DB_ROOT, CONFIG_ROOT, APP_CONFIG_PATH
QBIT_WEBUI_PORT, VAULTWARDEN_DOMAIN
TAILSCALE_API_KEY   # read by tailscale-metrics Go binary directly
BWS_ACCESS_TOKEN    # machine account token for BWS
```

## Manual secret lookup

```bash
# List all secrets
BWS_ACCESS_TOKEN="..." bws secret list

# Get a specific secret
BWS_ACCESS_TOKEN="..." bws secret get <secret-id>

# See all injected env vars (useful for debugging)
python3 scripts/bws-run.py printenv | sort
```

## Running docker compose manually with secrets

```bash
# Load BWS_ACCESS_TOKEN from .env, then inject secrets and run compose
export $(grep BWS_ACCESS_TOKEN .env | tr -d '"' | xargs)
python3 scripts/bws-run.py docker compose -f compose-mon.yml up -d
```

## Adding a new secret

1. Go to [Bitwarden Secrets Manager](https://vault.bitwarden.com/#/sm) → your project
2. Create a new secret:
   - **Plain string**: key = env var name, value = the secret value
   - **JSON group**: key = group name (e.g. `MEDIA`), value = `{"VAR_NAME": "value", ...}`
3. The secret is available on the next deploy automatically — no changes to `.env` or code needed

## Grafana monitoring

The Deploy Monitor dashboard shows a **Bitwarden Secrets** panel (green/red) indicating whether secrets were successfully loaded on the last deploy run.
