# Auto-fetch the next episode(s) on watch (Tautulli / Jellyfin → Sonarr)

Sonarr only searches by its own schedule and monitoring state: it has no idea what you just
watched. This adds that: when an episode is marked watched (in Plex via Tautulli, or in Jellyfin
directly), the next `WATCH_NEXT_MARGIN` episodes of that show get monitored and searched. Add new
shows with **Pilot** monitoring instead of "All Episodes" and the season fills in progressively
as you watch it, instead of grabbing everything on day one.

```
Tautulli (watched)  ──┐
                       ├──► http://watch-next:9010/hooks/{tautulli,jellyfin}?token=...
Jellyfin (webhook)  ──┘             │  (container on media-network, no published port)
                                     ▼
                          Sonarr API (monitor + EpisodeSearch)
```

Deletion/cleanup of already-watched episodes is explicitly out of scope: this only fetches
forward.

## Security

Unlike `deploy-webhook` (public internet, fronted by a Cloudflare Tunnel), `watch-next` is never
published: it has no `ports:` entry, so it's reachable only from other containers on
`media-network` via Docker's own DNS, never from the LAN or the internet. The shared
`?token=` check is defense-in-depth against another container on the same network, not the only
thing standing between it and anything external: there isn't anything external to worry about
here.

## Setup

### 1. Secret and containers (on the Pi, or via a normal push to `main`)

```bash
openssl rand -hex 32              # put it in .env as WATCH_NEXT_TOKEN
# WATCH_NEXT_MARGIN=3 is the default (also in .env.example); lower it for a tighter margin
export COMPOSE_ENV_FILES=versions.env,.env
docker compose up -d --build watch-next tautulli
```

If Tautulli is new, open `https://tautulli.platanosverdes.com` and run its setup wizard once to
pair it with Plex (`http://host.docker.internal:32400`).

### 2. Tautulli (Plex)

Settings → Notification Agents → **+ Add a new notification agent** → **Webhook**:

- Webhook URL: `http://watch-next:9010/hooks/tautulli?token=<WATCH_NEXT_TOKEN>`
- Webhook Method: `POST`
- Triggers tab: enable **Watched**
- Data tab (Watched): JSON payload, built from Tautulli's own variable picker:
  ```json
  {"tvdb_id": "{thetvdb_id}", "season": "{season_num}", "episode": "{episode_num}"}
  ```
  Every value must be quoted, season/episode included: Tautulli's Webhook agent parses the
  template *as typed* as JSON before substituting anything, so an unquoted `{season_num}` makes
  the raw template invalid JSON and Tautulli silently sends an empty body instead (no error
  surfaced anywhere except its own log). Confirm `{thetvdb_id}` in that same picker; the exact
  variable name has moved before across Tautulli versions.

### 3. Jellyfin

Dashboard → Plugins → Catalog → install **Webhook**, restart Jellyfin, then Dashboard → Plugins →
Webhook → **Add Generic Destination**:

- Webhook Url: `http://watch-next:9010/hooks/jellyfin?token=<WATCH_NEXT_TOKEN>`
- Notification Type: **Playback Stop**
- Item Type: **Episodes**
- Send All Properties: on (so `ProviderIds`, `SeasonNumber`, `EpisodeNumber`,
  `PlayedToCompletion` are all included; `watch-next` ignores anything it doesn't recognize)

### 4. New shows in Sonarr

When adding a show, pick **Pilot** in the monitor dropdown instead of "All Episodes": the whole
point of this service is to stop the season from grabbing in one shot. Shows already fully
monitored today are unaffected either way; retrofitting them to unmonitor future episodes is a
manual, separate decision.

## Troubleshooting

```bash
docker logs -f watch-next
```

- One line per watched episode: `<Title> S02E05 watched: monitored 3, searching 1`.
- `no series found for tvdbId ...`: Sonarr doesn't have that show at all, or the tvdb id in the
  webhook payload is empty, check step 2/3's template rendered correctly.
- `S02E05 not found among its episodes`: the season/episode numbers Tautulli/Jellyfin sent don't
  match what Sonarr has (common right after a show's episode order changes upstream).
- To test the Sonarr-side logic without waiting on real playback, POST a payload directly from
  another container on the network, e.g.:
  ```bash
  docker exec tautulli curl -s -X POST \
    "http://watch-next:9010/hooks/tautulli?token=<WATCH_NEXT_TOKEN>" \
    -d '{"tvdb_id": "<id>", "season": "1", "episode": "1"}'
  ```
