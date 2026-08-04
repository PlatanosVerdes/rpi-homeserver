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

## The dead man's switch

Every rule above dies with the Pi. A power cut, a dead SD card or an ISP outage produces silence,
and silence looks exactly like "everything is fine". So the logic is inverted: the Pi says "still
alive" on a schedule, and an **external** service alerts when those pings stop.

`scripts/heartbeat.sh` runs from cron every 5 minutes. It does not ping blindly: if Caddy or
Prometheus are not running it reports a failure instead, so a Pi that is powered on but broken
cannot keep sending a reassuring heartbeat.

With `HEALTHCHECK_URL` unset the script exits quietly, so a clone without an account is not broken,
just uncovered. To turn it on:

1. Create a free check at [healthchecks.io](https://healthchecks.io) and set **Period 1 minute,
   Grace 3 minutes**. Those two numbers, not the cron, decide how fast you are told: the check is
   declared down once `period + grace` passes with no ping, so this alerts about 4 minutes after
   the Pi goes dark. Going lower turns a single missed ping into a false alarm.
2. Put its ping URL in `.env` as `HEALTHCHECK_URL`.

Cost of running it every minute, measured on the Pi: **~0.2s of CPU** per heartbeat (0.12s user +
0.09s sys, so roughly 0.3% of one core) and no disk writes. The wall time is ~1s but almost all of
it is waiting on the network. For comparison, `tailscale-metrics` already runs every minute.

That is the only alert in this whole setup that does not depend on the Pi being alive.
