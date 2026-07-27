# Radarr / Sonarr config exports

Point-in-time JSON exports of the **custom formats** and **quality profiles** from Radarr
and Sonarr, committed to git so the hard-to-recreate scoring rules (AV1 reject, x264 prefer,
Castilian > VOSE > English language scoring, Bluray-over-Remux priority, the `CSWEB`
Chinese-hardsub penalty, etc.) survive a full disk loss.

The complete app state (databases, indexers, download clients, history) lives in `appdata/`
and is backed up separately (see [../../docs/backups.md](../../docs/backups.md)). These JSON
files are a lightweight, git-versioned safety net for just the tuning that is painful to
rebuild by hand.

**No secrets here:** indexers and download clients (which hold API keys / passwords) are
deliberately NOT exported. Custom formats and quality profiles contain only rules and scores.

```
radarr/custom-formats.json     radarr/quality-profiles.json
sonarr/custom-formats.json     sonarr/quality-profiles.json
```

## Regenerate (refresh these files)

Run against the live containers (from a machine with SSH to the Pi, or on the Pi itself):

```bash
for app_port in "radarr:7878" "sonarr:8989"; do
  app=${app_port%:*}; port=${app_port#*:}
  key=$(docker exec "$app" grep -oP '(?<=<ApiKey>)[^<]+' /config/config.xml)
  for ep in customformat:custom-formats qualityprofile:quality-profiles; do
    docker exec "$app" curl -s "http://localhost:$port/api/v3/${ep%:*}" -H "X-Api-Key: $key" \
      | python3 -m json.tool > "config/arr-exports/$app/${ep#*:}.json"
  done
done
# then: git commit -am "Refresh arr config exports"
```

## Restore

Fastest via the UI: Radarr/Sonarr → Settings → Custom Formats → **Import** (paste each object),
then recreate the quality profile and set the custom-format scores from
`quality-profiles.json`. Or POST each object back with `POST /api/v3/customformat` and
`POST /api/v3/qualityprofile`.
