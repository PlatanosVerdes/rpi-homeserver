# Alerting

Grafana has beautiful dashboards, but a dashboard only helps when you are looking at it. These
alerts are provisioned from git so they are reproducible and reviewable in a diff.

```
Prometheus ──► Grafana alert rules ──► Telegram bot (Raspi-Alerts)
                                              ▲
external dead man's switch ───────────────────┘  (covers the Pi being dead)
```

## What is provisioned

Everything lives in `config/grafana/alerting/` and is **read-only in the Grafana UI on purpose**:
this repo is the source of truth.

| File | What it does |
| :--- | :--- |
| `contact-points.yml.tmpl` | The Telegram bot Grafana sends to (rendered, see Credentials) |
| `policies.yml` | Route everything to that bot, group by alert, repeat every 6h |
| `rules.yml` | The rules themselves |

| Rule | Fires when | For |
| :--- | :--- | :--- |
| System disk almost full | less than 15% free on `/` | 10m |
| Data disk almost full | less than 10% free on the data disk | 10m |
| Backup failed | `backup_last_status != 0` | 5m |
| No recent backup | no successful backup in 36h | 15m |
| Deploy failing | last deploy ended in error | 5m |
| Prometheus target down | any scrape target unreachable | 5m |
| Pi running hot | SoC above 75 C | 10m |

Every expression uses PromQL's `bool` modifier so it returns 1 when it fires, which keeps all the
rules on the same `threshold > 0` condition instead of seven different reduce/threshold shapes.

## Credentials

`TELEGRAM_ALERT_BOT_TOKEN` and `TELEGRAM_ALERT_CHAT_ID` live in `.env` and are **never
committed**. Since this repo is public, neither value can sit in a provisioning file, so:

```
config/grafana/alerting/          in git: rules, policies, contact-point TEMPLATE
        │  deploy_control.sh renders it (render_grafana_alerting)
        ▼
appdata/grafana-alerting/         gitignored, what Grafana actually reads
        │  bind-mounted over /etc/grafana/provisioning/alerting
        ▼
Grafana
```

Grafana's own `$VAR` interpolation cannot do this job: it re-infers the type of the substituted
value, so an all-digits chat id comes back as a number while the Telegram integration demands a
string. Provisioning then fails, and a failed alerting provisioning **takes Grafana's whole
startup down**, not just the alert.

If `.env` has no Telegram credentials, the render step leaves the directory empty and Grafana
simply starts with no alerting. It never starts with a half-configured one, because a policy
pointing at a missing contact point is also fatal.

## Testing it

The bot only sends, so there is nothing to poll. To check the whole chain:

1. Grafana → Alerting → Contact points → `telegram-homelab` → **Test**. A message should arrive.
2. Or hit the API directly to confirm the credentials alone work:
   ```bash
   curl -s -X POST "https://api.telegram.org/bot<TOKEN>/sendMessage" \
     -d chat_id=<CHAT_ID> -d text="test"
   ```

## Adding a rule

Copy an existing block in `config/grafana/alerting/rules.yml`, give it a fresh `uid`, and write the expression so it
returns 1 when it should fire (`<something> > bool <threshold>`). Commit, and the deploy applies
it. A malformed rule is rejected at startup, so check `docker logs grafana | grep provision`
after deploying.

## Still missing: the dead man's switch

Every rule above dies with the Pi. If it loses power or its internet, Grafana is not there to
tell you. That needs something **outside** the house expecting a regular ping:

1. Create a free check at [healthchecks.io](https://healthchecks.io) with a period of 15 minutes
   and a grace of 10.
2. Put its ping URL in `.env` as `HEALTHCHECK_URL`.
3. A cron entry every 5 minutes curls it. When the pings stop, healthchecks.io emails you.

Not wired up yet: it needs the account created first.
