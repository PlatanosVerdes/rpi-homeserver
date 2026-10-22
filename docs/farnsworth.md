# Farnsworth, and AceStream live TV

## AceStream on this box

| Piece | Job |
| :--- | :--- |
| [bifrost](https://github.com/PlatanosVerdes/bifrost) (pinned as `BIFROST_VERSION`) | Reads the channel list from IPFS through `kubo`, writes Jellyfin's M3U (one stable URL per channel) and an XMLTV guide cut to those channels, and is the proxy Jellyfin plays from: it picks a live source per channel and shares one engine connection among everyone watching. It never opens a channel on its own: the Pi plays one at a time |
| `aceserve` | The engine, on the host network so its P2P uses the home line. Only its HTTPS goes through `gluetun` (`https_proxy`), because Spanish ISPs intercept the AceStream API during matches |
| Jellyfin | Live TV tuner (M3U) `/config/acestream/channels_ace.m3u`; the XMLTV provider is pointed at `/config/acestream/guide.xml` by the deploy (`scripts/sync/jellyfin-guide.sh`) |
| farnsworth | The page below, for VLC |

```bash
docker exec bifrost wget -qO- http://127.0.0.1:6879/api/status   # list, what is playing, recent opens
docker logs -f bifrost
```

The second node runs the same AceStream subset ([nodes.md](nodes.md)).

## What farnsworth is for

Acestream channels play fine on the TV app and in VLC, and badly in a browser. Jellyfin reports the
reason itself: `The container is not supported`. The channels arrive as MPEG-TS, which browsers
cannot play, so Jellyfin repackages them, and because a P2P stream starts mid-broadcast with no
headers it cannot confirm the video is compatible either, so it re-encodes the whole thing to be
safe. Measured on this Pi with a 20 Mbps 1080p feed: **16 fps, 0.32x**. Real time needs 1.0x, so the
player empties its buffer and restarts every few seconds.

Nothing on the server fixes a 0.32x. This page skips the browser instead: it lists the channels and
hands one to VLC, which plays MPEG-TS natively and needs no repackaging at all.

Named for Philo Farnsworth, who built the first working electronic television in 1927, aged 21.

## How it works

```
bifrost /api/channels ──► channel names and current sources
                                     │ (fetched at most once a minute)
                                     ▼
   phone / laptop ──HTTPS──► Caddy ──► farnsworth :8086
                                     │  serves the page, logs the play
                                     ▼
                                    VLC
                                     │
                                     └──direct──► aceserve :6878
```

**No video passes through this service.** It hands out a URL and steps aside, so it cannot become a
bottleneck and uses no bandwidth of its own.

| Endpoint | What it does |
| :--- | :--- |
| `/` | The channel list, grouped as the playlist groups them |
| `/m3u/<id>` | A one-channel playlist, `audio/x-mpegurl`, which desktop players open directly |
| `/click/<id>` | A beacon. VLC's URL scheme never reaches a server, so the page reports the play here first |

## Three decisions worth knowing

**It points at the engine, not at bifrost's proxy.** The proxy holds a request until video actually
arrives, so VLC would sit on a black screen for up to 45 seconds with no feedback and might give up
on its own. Against the engine, VLC gets the redirect immediately and shows its own buffering, which
is the same wait but a visible one.

**`STREAM_BASE` must be an address the client can reach.** The page is served through Caddy, but the
stream is fetched by VLC on a phone. `aceserve:6878` means nothing outside Docker's network, so it
is built from `STATIC_IP`, reachable from the LAN and over the tailnet.

**The play is logged here.** This is the only place that sees the real client: through Jellyfin every
request looks like Jellyfin's container. That makes "who watched what" answerable.

```
[play] Dazn MotoGP 1080p * (20d4b358) via m3u from 100.x.x.x
```

## Configuration

| Variable | Default | Notes |
| :--- | :--- | :--- |
| `LISTEN_ADDR` | `:8086` | Not published on the host; Caddy reaches it over `media-network` |
| `STREAM_BASE` | `http://192.168.1.180:6878` | Built from `STATIC_IP` in `compose-media.yml` |
| `BIFROST_URL` | `http://bifrost:6879` | Where the channel list comes from: `/api/channels`, fetched at most once a minute |

## If something looks wrong

- **The list is empty.** bifrost did not answer. Check `docker logs farnsworth` and that
  `docker exec farnsworth wget -qO- http://bifrost:6879/api/channels` returns channels.
- **The VLC button does nothing.** The `vlc-x-callback://` scheme needs VLC installed on that device.
  The `.m3u` link underneath works everywhere and is the fallback.
- **VLC opens and sits there.** That channel has no one sharing it right now. Try the other entry
  for the same channel: several appear twice from different sources.
- **A channel plays here but not in Jellyfin.** Expected, and the reason this page exists.
