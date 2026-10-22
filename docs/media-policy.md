# Media and download policy

The library is tuned for how it is actually watched, not for maximum file fidelity. This is the set
of rules and the reason for each, so none of them is mistaken for misconfiguration. What happens to
a file after it is downloaded, and when it is deleted, is [lifecycle.md](lifecycle.md).

## Playback targets

| Screen | Where |
| :--- | :--- |
| Samsung QLED TV (Tizen), the primary screen, high resolution | on the LAN |
| iPad, secondary | anywhere, over the tailnet |

Plex runs on the Pi, which cannot transcode video in real time, so files must **direct-play**.

| What the TV does with it (from Plex's logs) | Codecs |
| :--- | :--- |
| Direct-plays | H.264, HEVC, 4K, HDR10, EAC3/AC3 audio |
| Fails: it cannot decode it and the Pi cannot transcode it in time ("Not enough CPU") | **AV1**, **VC-1** |
| Audio-only transcode, which the Pi handles | DTS, TrueHD ([audio-convert.md](audio-convert.md) adds an AC3 track for those) |

Rule of thumb for smooth playback: 1080p, x264/H.264, AAC or EAC3/AC3, text subtitles (SRT / mov_text).

## Download rules

Radarr and Sonarr custom formats and quality profiles, committed in `config/arr/` and pushed into
both apps on every deploy by `scripts/sync/arr-config.sh`. A change made in the apps' UI is reverted
within 30 minutes; profiles are matched by name, so renaming one creates a second.

- **Reject AV1**: unplayable here.
- **Prefer x264/H.264 1080p encodes**: small, universal, and about 99% of the quality.
- **Bluray-1080p over Remux-1080p**, with Remux kept as a fallback so a title is never left
  undownloaded. The tools' default prefers Remux; a Remux is a 30-80 GB copy of the disc whose extra
  quality is invisible on a 55-inch TV or an iPad, against 5-15 GB for the encode.
- **Penalise** Remux and heavy lossless audio (DTS-HD): quality the devices cannot use.
- **Language:** Castilian Spanish > VOSE (original audio, Spanish subtitles) > English >
  Latin-American Spanish (avoided).

### RAR packs are rejected outright

`RAR pack (reject)` scores **-20000**. A packed release is seeded as its `.rNN` archives while Plex
plays the `.mkv` unpackerr extracts, so the film sits on disk twice until the tracker is paid, and no
hardlink can fix that: the extracted file is new bytes. Nothing in a release title says "packed", so
the format matches the groups that did it here (GAZPROM, CEBRAY, GAZER), and the list grows as more
turn up. At -300 it lost: one film once held 197 GB in two packed releases plus the extracted copy.

-20000 and not -6000, because a release is rejected only when its **total** falls below
`minFormatScore` (-6000), so positives offset a penalty: a Spanish-audio packed release (+3000 or
more) would get through a -6000 penalty. Nothing this profile awards can offset -20000.

The trade-off: a film that exists only as a packed release is not downloaded at all. That is the
choice, an empty shelf over 197 GB.

### When a film is finished

Radarr has two independent gates, and the UI calls both a cutoff:

| Gate | Setting | Decides |
| :--- | :--- | :--- |
| 1, quality | the profile's cutoff: `Bluray-1080p` on HD-1080p, `Bluray-2160p` on the other two | whether the film is listed in Cutoff Unmet |
| 2, score | `cutoffFormatScore` 2200 on all three | whether RSS keeps looking for an upgrade |

A film with Bluray-2160p scoring 400 is not in Cutoff Unmet (gate 1 met); a WEBDL-2160p scoring
3400 is (gate 1 not met). **Gate 1 decides the list.**

`cutoffFormatScore` 2200 is Spanish audio plus x264. It used to be 10000, which no release can
reach (the positive formats add up to +6700 at most, several are mutually exclusive, and a good
release scores about +3200), so every film stayed in permanent upgrade search, and each upgrade
left the previous release seeding its tracker debt. It is a **target** (stop when reached), where
`minFormatScore` is a **floor** (reject below it).

The remaining churn is deliberate: a film that exists only in English scores 700 and keeps being
searched, because Castilian audio is worth +2000 and worth waiting for.

## Around the downloads

| Tool | Job |
| :--- | :--- |
| Bazarr | Spanish subtitles for the whole library. `scripts/sync/bazarr-prune-subs.py` (05:40) removes the rows it keeps for files Radarr already replaced |
| Unpackerr | Extracts scene RAR releases, which the arrs cannot read and would leave in `importPending` forever. Archives stay so the torrent keeps seeding; the extracted copy is removed once imported |
| Maintainerr | Deletes a watched film two days later ([lifecycle.md](lifecycle.md)) |
| watch-next | Fetches the next episodes as you watch ([watch-next.md](watch-next.md)) |

Wiring, once per install: Prowlarr → Settings → Indexers → proxy `http://flaresolverr:8191`;
Prowlarr → Apps → Radarr and Sonarr with their API keys; in each arr, download client qBittorrent at
`qbittorrent:8080`. The links between Seerr, Bazarr and the arrs are pushed by
`scripts/sync/arr-links.sh`.

## qBittorrent

Everything here is in `config/qbittorrent/preferences.json` and reapplied on every deploy by
`scripts/sync/qbit-config.sh`. Change the JSON, not the WebUI: the next deploy converges the app back.

| Setting | Value | Why |
| :--- | :--- | :--- |
| `dl_limit` | 10 MiB/s | The line is not the limit (about 770 Mbps); the storage is. `/mnt/data` is mergerfs (FUSE) over two USB spinning disks, so every written byte costs CPU. Across 14 days, load crossed the 4 cores at about 10 MB/s, and one night of 226 GB of upgrades held load at 16 for 100 minutes. 10 MiB/s still clears 50 GB in about 85 minutes |
| `max_active_downloads` | 1 | TorrentLeech's entry class grants one slot and a partial download holds it ([private-trackers.md](private-trackers.md)) |
| `max_active_torrents`, `max_active_uploads` | 100 / 100 | A queued torrent is not seeding, and seed time only counts while active; at 18 slots half of them waited. Not unlimited, so a much larger library cannot open hundreds of connections |
| `async_io_threads` | 4 | Fewer requests in flight, so the platters' heads travel less. Reasoned from the hardware, not measured |

`alt_dl_limit` is dead config: it only applies while `scheduler_enabled` is true, and it is false.

### The BitTorrent port

`QBIT_BT_PORT` in `.env`, deliberately outside 6881-6889 (the obvious range for an ISP to throttle).
Three places must agree, and nothing warns when they do not:

| Where | How it gets there |
| :--- | :--- |
| qBittorrent's own setting | `scripts/sync/qbit-config.sh`, from `.env` |
| the published container port | `compose-arrs.yml`, tcp **and** udp |
| the router forward | by hand, to the Pi's static IP, tcp and udp |

When they disagreed (qBittorrent moved, compose still publishing 6881), inbound arrived at a port
nothing listened on, while qBittorrent kept reporting `connected`: outgoing connections were never
affected. Check inbound with the peer flags, never the status field
([private-trackers.md](private-trackers.md#inbound-the-only-measurement-that-matters)).
