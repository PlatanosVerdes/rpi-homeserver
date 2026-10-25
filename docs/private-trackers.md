# Private trackers

The rules each private tracker sets, how a tracker is set up on this box, the loop that keeps the
accounts safe, and the indexers in use. What happens to a torrent after download, and when it is
deleted, is [lifecycle.md](lifecycle.md); the seed goal per tracker lives there and in
`config/qbit-manage/config.yml`.

## The policy in one paragraph

On a public tracker seeding buys nothing: there is no account and no ratio rule, only this address
sitting in a public swarm, so a public torrent goes two hours after it finished. On a private tracker
seeding **is** the currency, and deleting early is the most expensive mistake available: an unpaid
torrent becomes a hit & run, and a few of those disable an account. **Nothing is ever deleted while
a tracker is still owed**, and every seed goal is set above what the site asks, because the site's
clock runs behind the client's. Tag a torrent `keep` and no group touches it.

What each site pays for is not the same, so the deletion rule reads the site rather than applying one
number everywhere:

| Tracker | What pays | When a torrent may go |
| :--- | :--- | :--- |
| TorrentLeech | upload only | hit & run cleared (240 h or 1:1), then once quiet for a day |
| DigitalCore | upload, plus a little for holding (leech bonus) | 120 h or 1:1, so it rotates faster than TorrentLeech |
| C411 | upload only | their hit & run is disabled site-wide, so once quiet, past the margin |
| retrotoon | seeding points, no ratio rule | their 72 h, then once quiet |

## Three numbers, before touching anything

**Ratio is the tracker's number, not the client's.** Freeleech and upload multipliers make them
differ wildly: DigitalCore once showed ratio 2.76 on site while qBittorrent showed 0.006. Always read
the site's own profile page (or tracker-control's reading of it).

**The buffer, not the ratio, governs a decision.** How many GB can still be downloaded before
crossing the line the site disables accounts under:

```
buffer   = uploaded - min_ratio x downloaded
headroom = buffer / min_ratio        <- GB of non-freeleech downloads that still fit
```

Freeleech never moves `downloaded`, so it can only help. That is why "freeleech only" is the remedy
for a thin ratio, and why the grabber can keep running while the arrs are held back.

**Hit & run is a separate way to lose the account.** It is per torrent: fail to seed one back for
the required time or ratio and you collect a warning whatever the overall ratio. The site counts
seeding from announces, so a paused, errored or file-missing torrent pays nothing off, which is why
the alert here is "owed with the clock stopped", not "how many are owed".

## How a tracker is set up on this box

In the order they matter:

**1. Prowlarr, where filtering belongs.** Prowlarr full-syncs its indexers to Radarr and Sonarr, so
anything switched in an arr is overwritten on the next sync. Most Cardigann definitions carry a
**`Search freeleech only`** checkbox, which every app searching through Prowlarr inherits:

```bash
docker exec prowlarr sh -c 'sed -n "/^settings:/,/^login:/p" /config/Definitions/<site>.yml'
```

**2. Radarr's `requiredFlags`, a second belt.** `requiredFlags = [1]` is `G_Freeleech` and survives
the Prowlarr sync. **Sonarr has no such field**, which is why step 1 is the real control. The API
rejects the save with a 400 while the indexer is unreachable; `?forceSave=true` gets around it.

**3. Seed goal: a `share_limits` group in `config/qbit-manage/config.yml`**, plus the announce host
under `tracker:` so torrents get the tag the group selects on. Met by seeding time **or** ratio,
whichever comes first (`max_ratio: -1` where the site has no ratio rule). Set the time above the
site's requirement: a goal set exactly at it once came three hours from deleting a torrent that
still owed 88.

```yaml
torrentleech:
  priority: 4
  include_all_tags: [torrentleech]
  include_any_tags: [noHL, ratio, cross-seed]
  exclude_any_tags: [keep, keep-bonus]
  max_seeding_time: 15d     # 240 h asked, 120 h of margin
  max_ratio: 1.2
  min_last_active: 1d
  cleanup: true
```

**4. `config/trackers/rules.json`**, what tracker-control reads: minimum ratio, hit & run rule, the
announce hostnames, the free-space floor, and the tiers. Sites disagree on when the clock starts and
whether a ratio clears anything, so the hit & run rule has three optional fields, each in the site's
own terms:

```json
"hit_and_run": {
  "min_seed_hours": 72,
  "min_ratio": null,          // null where the site has no ratio rule: only time clears
  "grace_hours": 24,          // hours before their clock starts, added to what is required
  "starts_at_progress": 0.1   // when the obligation begins, if not at completion
}
```

Exemptions (C411's for a global ratio over 2.0, the first three torrents, anything under 100 MB) are
left out on purpose: they only ever reduce what is owed, and owing more is the safe direction.

**5. autobrr, for the announce channel.** Freeleech is worth having the second it is announced.
**Identification → Mechanism must stay `None`**: SASL or NickServ makes the bot reconnect every second
or two, and sites ban for it. The nick is `<username>_bot` unless the site says otherwise.

**6. Verify, because two of these fail silently:**

```bash
# does the freeleech filter apply? every result should carry the flag
curl -s -H "X-Api-Key: $KEY" \
  "http://localhost:9696/api/v1/search?query=1080p&indexerIds=<id>&type=search" \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); \
    print(len(d), sum(1 for r in d if r.get("indexerFlags")))'

# what the site says about the account, and what the automation decided
docker logs --since 1h tracker-control
docker exec tracker-control wget -qO- localhost:9111/metrics | grep '^tracker_'

# what it would change, writing nothing (a second copy; stop it with Ctrl-C after the first pass)
COMPOSE_ENV_FILES=versions.env,.env docker compose run --rm -e DRY_RUN=1 tracker-control
```

Read the value back rather than trusting an exit code: that habit is how a sync that reported
success while applying nothing was found.

## The control loop

![Ratio control loop](diagrams/ratio-loop.png)

tracker-control reads every account each 30 minutes and acts in the same pass. **Only TorrentLeech
is steered**: it is the only site with tiers and a free-space floor in `config/trackers/rules.json`.
DigitalCore, C411 and retrotoon are read and alerted on, never steered. The tier comes from the
headroom, and this table is the single source for it:

| Headroom | Prowlarr | Radarr | autobrr |
| :--- | :--- | :--- | :--- |
| under 25 GB | freeleech results only | `requiredFlags = [1]` | 3 grabs a day |
| 25 to 100 GB | freeleech results only | `requiredFlags = [1]` | 2 grabs a day |
| over 100 GB | all results | `requiredFlags = []` | 1 grab a day |

More grabs the less headroom is left: with little headroom the account needs to build ratio, and
freeleech raises the dividend without touching the divisor. Nothing is written when the value already
matches, so it is silent except at a crossing, and every change goes to Telegram.

- **The grab rate is a disk budget.** A grab cannot be deleted until its hit & run window closes, so
  the disk cost is rate times retention: 2 a day at ~20 GB over a 15-day goal holds ~600 GB. Below
  `min_free_gb` (400 GB) the grabber is paused whatever the ratio says, with 50 GB of hysteresis.
- **How each site is read.** TorrentLeech's login comes from Prowlarr, which already holds it.
  DigitalCore and C411 are API-key indexers with no login to reuse: DigitalCore reads with a session
  cookie seeded from a browser (2FA), C411 with `C411_USER`/`C411_PASSWORD` from `.env` (Prowlarr only
  as a fallback). retrotoon's login sits behind Cloudflare
  Turnstile, so it stays hand-read in `config/trackers/readings.json`, which is also every site's
  fallback. Session cookies live in `appdata/tracker-stats/`, so a site sees about one login a day.

Exported: `tracker_ratio`, `tracker_min_ratio`, `tracker_uploaded_bytes`, `tracker_downloaded_bytes`,
`tracker_buffer_bytes`, `tracker_headroom_bytes`, `tracker_points`, `tracker_warning_seconds`,
`tracker_hnr_pending`, `tracker_hnr_at_risk`, `tracker_hnr_torrent_hours_left`,
`tracker_tier_grabs_per_day`, `tracker_tier_freeleech_only`, `tracker_grabber_paused_no_disk`. Four
alerts sit on them: headroom under 10 GB, ratio below the line, an obligation with the clock
stopped, and no reading in two hours. The last matters most: a stopped reader looks exactly like a
healthy account.

## Quiet while watching

A Pi 4 does not have the CPU or the line to download at full speed and serve video at the same
time: with two channels playing, qBittorrent took a quarter of a core and the load sat at 17 on four
cores. So tracker-control also watches Jellyfin, every 30 seconds, apart from its 30-minute loop:

- **Something plays on Jellyfin** (a film, an episode, a live channel from bifrost; paused sessions
  do not count): it turns on qBittorrent's **alternative speed limits**, the turtle in its web UI,
  set to `QUIET_DOWN_KIB` down and `QUIET_UP_KIB` up (3072 and 1024 KiB/s by default).
- **Nothing has played for 2 minutes**: it turns them off again. The 2 minutes cover a pause, a
  channel change or the next episode, so the limits do not flap.
- **Throttled, never paused.** Seeding goes on, slower: the accounts above still need the upload,
  and a paused torrent stops counting towards a hit & run window.
- **It only lifts limits it set.** If the turtle was turned on by hand, it stays on.
- **What it does not see:** a stream watched straight from bifrost or Plex, not through Jellyfin.

The limits are written to qBittorrent's preferences when the service starts, so changing the
variables in `.env` and redeploying is how to tune them. `QUIET_WHILE_WATCHING=off` turns it off. It
needs `JELLYFIN_API_KEY`; without a valid key it logs `quiet: Jellyfin: /Sessions: 401` and changes
nothing.

Its log lines start with `quiet:` (in VictoriaLogs: `container:tracker-control "quiet:"`), and it
exports `tracker_control_quiet` (1 while it holds the limits on) and
`tracker_control_jellyfin_playing` (sessions playing at the last look).

## TorrentLeech

| Rule (from staff in `#tlhelp`) | Value |
| :--- | :--- |
| Minimum ratio | **0.4.** Below it: a warning, seven days, then disabled |
| Hit & run | 240 h of seeding **or** ratio 1:1 per torrent. **Three uncleared = disabled** |
| Freeleech | does not count towards `downloaded` |
| Download slots | one at the entry class, and a partial download occupies it |
| Bonus points | 1 point = 14.3 MB of upload credit, paid per GB held per hour |
| On being disabled | **the passkey is reset**: every torrent needs its announce URL rewritten (`addTrackers`/`removeTrackers`) |
| Support | IRC `#tlhelp`: the username, the account email in private, and a speedtest link |

| Configured here | Value | Why |
| :--- | :--- | :--- |
| Prowlarr | `Search freeleech only`, driven by the tier | one switch every app inherits |
| Radarr | `requiredFlags = [1]` | second belt |
| Sonarr | indexer left on at every tier | the site has series, and Prowlarr already filters |
| Seed goal | 15 d or ratio 1.2 | 120 h above the 240 h obligation: their clock lagged the client's by up to 118 h |
| autobrr | `TL freeleech ratio builder`: freeleech, 5 to 40 GB, 2 to 3 a day, no category, tag `ratio` | early seeding is where upload comes from |
| qBittorrent | `max_active_downloads: 1` | one slot: grabs queue instead of jamming it |

**No category on the grabber's action is deliberate.** A category makes Radarr adopt a download it
cannot import. These grabs belong to nothing; the `ratio` tag says so, and is what makes them visible
to a share-limit group, since without a category the hardlink check never inspects them.

Three autobrr API traps, each of which cost an hour:

- In a qBittorrent action **`label` does nothing** (it is Deluge's field); tags come from `tags`.
- **`POST /api/filters` fails** with `NOT NULL constraint failed: filter.resolutions` unless every
  list field is present, and stores neither actions nor the indexer link: those need
  `POST /api/actions` and a follow-up `PUT /api/filters/{id}` with nulls stripped.
- **`PUT /api/actions/{id}` detaches the action** unless the body carries `filter_id`, which the GET
  does not return. The filter then matches and pushes nothing; the only trace is
  `no active actions found for filter` in autobrr's log. tracker-control asserts
  `actions_enabled_count >= 1` on every run.

Links: [warnings](http://wiki.torrentleech.org/doku.php/everything_about_warnings),
[common mistakes](https://wiki.torrentleech.org/doku.php/common_mistakes),
[autobrr](https://wiki.torrentleech.org/doku.php/autobrr),
[maintaining ratio](https://forums.torrentleech.org/t/how-to-maintain-your-ratio/78082/).

## DigitalCore

| Rule | Value |
| :--- | :--- |
| Minimum ratio | **0.5**. Below it, five days of ratio watch, then leeching is revoked (the account stays) |
| Hit & run | starts at **10% downloaded**, clears at **5 days of seeding or 1:1**. Freeleech is not exempt |
| Detection | **one hour without an announce** lists it; 10 days to fix before it becomes permanent |
| Penalty | 5 is a warning, more is a download ban |
| Whose clock | **the site's only**: seed time is logged when an announce succeeds |
| Free automatically | every new torrent for 24 h, and **anything 15 GB or larger** |
| Leech bonus | **10 GB actively seeded = 1% off every download**, 7-day average; 1 TB is site-wide freeleech. Only 50 GiB per torrent counts, scaled by `1 + 1/seeders` |
| Banned clients | uTorrent (except 2.2.1), BitComet, Azureus and others. qBittorrent is fine |
| Inactivity | 90 days without signing in disables the account |
| Cross-seeding | explicitly allowed |
| Modifying their torrents | **forbidden**: no extra trackers, no DHT, no PEX, no debrid |
| VPN | allowed; the site has its own proxy at `prxy.digitalcore.club` |

Their header shows the buffer at ratio 1.0, not at the 0.5 that decides anything; compute headroom
with 0.5.

| Configured here | Value | Why |
| :--- | :--- | :--- |
| Prowlarr | API key, FlareSolverr, no freeleech switch | an API definition, with no checkbox to flip |
| Seed goal | **180 h / ratio 1.0** | 60 h above their 5 days: their clock ran 9 to 23 h behind the client's |
| cross-seed | included | allowed, and free ratio |
| Account reading | `fetchDigitalCore`, with a session cookie | the API key reaches no profile data, and the account has 2FA |

**Reading the account.** Log in from a browser once and drop that session into
`appdata/tracker-stats/digitalcore-cookies.json`. When it expires, `tracker_up` goes to 0 for the
site and the cookie is seeded again.

**A passkey reset means re-downloading the `.torrent`, never editing it.** Their rules forbid
modifying torrent files, so the announce-URL rewrite used on TorrentLeech is not allowed here.

**`My Seeds` settles any argument about seed time**: it lists `Seed Time`, `Seed Time Left` and
`HnR` per torrent, the site's own accounting. Open obligations are not violations: a recent torrent
is owed by definition. The site only lists a hit & run once a torrent stops announcing for an hour,
which is what `tracker_hnr_at_risk` watches. A cross-seed starts their clock at its first announce,
so it shows far less than the client; cross-seeds have their own group anyway.

### Why the leech bonus is NOT bought with disk

Not deleting on this tracker would grow the bonus. Measured, of a 27% bonus, 9% came free from
torrents sharing inodes with the library and 18% was bought with 179 GB of separate copies, on an
account with plenty of headroom: a discount on a problem that did not exist, paid with disk the
TorrentLeech grabber needed. So the bonus grows only through hardlinks (library copies and
cross-seeds), which cost nothing. `bonus_hold` (the `keep-bonus` tag) stays in tracker-control,
unconfigured, for the day the arithmetic reverses.

### The grabber

| Piece | Value | Why |
| :--- | :--- | :--- |
| IRC | `irc.digitalcore.club:7000`, TLS, `#announce`, announcer `ENDOR` | they support autobrr by name |
| Nick | exactly the site username, case sensitive | their FAQ; idling in `#digitalcore` also pays points |
| Bot mode | on (usermode `+B`) | their IRC rules require a bot to identify itself |
| Auth | NickServ empty; invite through `ENDOR !invite <user> <irckey>` | a bot authenticates with the IRC key |
| Filter | `DC freeleech ratio builder`: freeleech, 15 to 30 GB, **1 a day** | below |
| Action | qBittorrent, no category, tag `ratio` | same as TorrentLeech |

15 GB is the floor because anything that size is automatically free here, so a grab cannot cost
ratio; 30 GB keeps one release from eating the budget and sits under the 50 GiB the bonus counts.
At one a day held for the 180 h goal that is about 165 GB of steady disk, for an account that does
not need the ratio: if disk gets tight, this is the first thing to turn off.

Four API traps, if it is ever rebuilt: `POST /api/indexer` wants `settings` as an **object**;
creating the indexer does not create its IRC network (`POST /api/irc`); a channel created that way
arrives **disabled** and is never joined; the network is updated at `PUT /api/irc/network/{id}`, not
`/api/irc/{id}`.

## retrotoon.world

| Rule | Value |
| :--- | :--- |
| Seed time | **72 h per torrent, within 10 days** of the download. Their strictest rule, no exceptions |
| Minimum ratio | none stated: the obligation is time |
| Content | animation only, strictly family-friendly; adult content is a permanent ban |
| Bonus | seed-bonus points, tripled on the daily featured torrent |

| Configured here | Value | Why |
| :--- | :--- | :--- |
| Prowlarr | `Generic Torznab` | no site-specific switch |
| Seed goal | **120 h, no ratio limit**, on announce host `ann.retrotoon.world` | 48 h of margin; a ratio limit would delete a torrent still owing time |
| Account reading | by hand, in `config/trackers/readings.json` | the login sits behind Cloudflare Turnstile |
| autobrr | indexer, passkey and IRC (`irc.retrotoon.world:6697`, bot mode) in place; filter **disabled** | cartoons only, and disk is the scarce thing. Enabling it is one toggle |

## C411

| Rule | Value |
| :--- | :--- |
| Minimum ratio to download | **0.8**; below it downloads are blocked until it recovers |
| Signup credit | **50 GB of upload**, counted in the ratio, one-off |
| Hit & run | **disabled site-wide** for now |
| When it returns | 72 h **or** 1:1 per torrent, after a 24 h grace; 3 active violations block downloads, 5 strikes is a ban |
| Cross-seeding | explicitly allowed |
| Freeleech | 0x, 50% and 2x upload, per torrent, per account or site-wide |
| Cheating | announced upload is checked against real swarm activity |
| Terms | never share the passkey or announce link (nothing in this repo holds one); a VPN or proxy is allowed, using one to cheat the ratio is not; sanctions are progressive |

The credit is why the ratio looks better than it is: of the uploaded figure, 50 GB never moved.

**Reading the account.** The API keys are scoped to Torznab, upload and drafts, with nothing for
account data, so `fetchC411` logs in with `C411_USER`/`C411_PASSWORD`: the csrf token from the login
page's `<meta name="csrf-token">`, then `/api/auth/login`. The profile page is a Nuxt app drawn in
the browser, so the HTML holds no figures; the reader calls `/api/auth/me`, which answers JSON
(`uploaded`, `downloaded`, `ratio`, `uploadCredit`, `minRatioForDownload`). What its errors mean:

```
c411: login returned 401: Nom d'utilisateur ou mot de passe invalide     <- the password in .env is wrong
```

A failed login means the row is stale, not that the tracker is broken: the panels fall back to
`config/trackers/readings.json`.

| Configured here | Value | Why |
| :--- | :--- | :--- |
| Prowlarr | API key, **no proxy**, direct like every other indexer | see [decisions.md](decisions.md#searching-c411-through-a-socks-proxy-stopped) |
| Seed goal | 150 h / ratio 1.0 | 72 h plus a 24 h grace is 96 h owed; 54 h of margin, so nothing trips their H&R when it returns |
| H&R measurement | on, as if their system were enabled | it is documented as returning |
| cross-seed | included | |

The definition exposes no freeleech facet (only `multilang`, `multilanguage`, `vostfr`), so the tier
has no lever here. The rule stays manual: **on C411, freeleech only.**

Every outage so far has been one of three, all indistinguishable from a ban until checked:

| What broke | How it reads | What fixes it |
| :--- | :--- | :--- |
| SOCKS proxy credentials expired | `Failed to authenticate with the SOCKS server` | historical: the proxy is off |
| The ISP intercepts the route on a match night | `Certificate validation for c411.org failed. RemoteCertificateNameMismatch`, other trackers down at the same time | nothing while it lasts; `scripts/ops/indexer-retry.py` clears Prowlarr's backoff within 15 min of the site answering ([alerting.md](alerting.md#an-indexer-down-on-a-match-night-is-the-line-not-the-tracker)) |
| The site is in maintenance | Prowlarr cannot parse the answer as XML | waiting |

None touches the announce path, so the torrents keep seeding through all three. The expensive
mistake on this site is deleting a torrent, not losing a search for an evening.

## BTSCHOOL, dropped on 2026-08-24

Its newbie assessment (50 GB up, 50 GB down and 6000 points in three days, from zero) was not met,
and every trace is gone from config. If a NexusPHP site with generous promotions comes up again, ask
whether the assessment can be met **before** signing up.

## Adding a new private tracker

One site per sitting; its section here ends up shaped like TorrentLeech's: rules, configuration, why.

1. **Read its rules pages** and answer the six questions: minimum ratio, what triggers a hit & run
   and how it clears, minimum seed time, download slots for the class, how freeleech is flagged, what
   the bonus shop sells. Write them here before configuring anything.
2. **Check the indexer works before believing anything else.** A dead proxy, a missing definition
   and a disabled account look identical from outside:

   ```bash
   K=$(sed -n 's/.*<ApiKey>\([^<]*\)<.*/\1/p' appdata/prowlarr/config.xml | head -1)
   curl -s -H "X-Api-Key: $K" http://localhost:9696/api/v1/indexer/<id> > /tmp/ix.json
   curl -s -X POST -H "X-Api-Key: $K" -H 'Content-Type: application/json' \
        --data-binary @/tmp/ix.json http://localhost:9696/api/v1/indexer/test   # the real error
   curl -s -H "X-Api-Key: $K" http://localhost:9696/api/v1/health               # how long it has failed
   ```

   **A proxy cannot be checked through Prowlarr's API**: it returns the proxy password as eight
   asterisks, and a GET-modify-PUT round trip writes the mask in as the real password. Ask the SOCKS
   server itself:

   ```bash
   curl -x socks5h://USER:PASS@nl.socks.nordhold.net:1080 \
        -s -o /dev/null -w '%{http_code}\n' https://<the site>/
   # any HTTP code: it authenticated and got out
   # curl: (97) User was rejected by the SOCKS5 server: the pair is dead
   ```

   The pair is NordVPN's **service credentials** (Nord Account → NordVPN → Manual setup), not the
   account login. `NORDVPN_USER`/`NORDVPN_PASS` in `.env` do not reach Prowlarr, which keeps its own
   copy in `appdata`.
3. **The freeleech switch** in its definition, on if the ratio is thin.
4. **`config/trackers/rules.json`** (hostnames, rules) and a **`share_limits` group** in
   `config/qbit-manage/config.yml` with its announce host under `tracker:`, the time above what the
   site asks.
5. **autobrr**, if it has an announce channel: Mechanism `None`, and size the rate as a disk budget.
6. **cross-seed** (`config/cross-seed/config.js`): a tracker that has what you already seed is free ratio.
7. **Verify**: `docker logs tracker-control` shows the site's own numbers, and a `DRY_RUN=1` run
   picks the tier you expect.

## cross-seed, the free ratio

The same bytes, already on this disk, seeded on every private tracker that also has the release.
Matches are hardlinked, so a cross-seed costs an inode and starts at 100%.

| Setting (`config/cross-seed/config.js`) | Value | Why |
| :--- | :--- | :--- |
| `torznab` | the private indexers only | a public cross-seed buys nothing |
| `torrentDir` | qBittorrent's `BT_backup`, read-only | what the client holds is the search list |
| `linkDirs` | `/data/downloads/cross-seed` | must be a path qBittorrent has mounted, and **cannot sit inside `dataDirs`**, hence no `dataDirs` |
| `duplicateCategories` | `true` | injects as `<category>.cross-seed`, so Radarr never sees a torrent it cannot import |
| `skipRecheck` | `false` | a flexible match can be a near miss; seeding bad data is worse than not seeding |
| `searchCadence` | `1 day` | 30 s between queries, on trackers with request limits |

torrent-drop also asks cross-seed about every torrent the moment it completes ([lifecycle.md](lifecycle.md#added-by-hand)).

Version facts: cross-seed **6.13.2 cannot log into qBittorrent 5.2.3** (a successful login answers
`204`, which it treated as failure; 6.13.7 handles it). `excludeOlder` takes vercel `ms` strings:
`180d` works, `6 months` does not.

A cross-seeded file keeps a link count above one while the second torrent exists, so bytes that would
be reclaimed after watching wait for the cross-seed's own term: the `cross-seed` group, 15 days and no
ratio limit, since it downloaded nothing and any ratio limit would fire on its first served byte.

## Where upload comes from

You only upload when somebody wants the same torrent **and picks you**:

1. **Be early.** A release minutes old has many leechers and few seeders. This is the reliable one,
   and why autobrr sits on IRC.
2. **Be the only one.** A large niche torrent nobody else seeds: everyone who wants it takes it from
   you. A 73.8 GB disc set with 1 seeder and 33 leechers beat a popular 5 GB release with 50 seeders.

Seeding an old catalogue does not work: 16 torrents with 20 to 259 seeders each produced 0 KB/s
over a day.

## Inbound, the only measurement that matters

- **Downloading is outbound**: you open the connection to whoever has the file. It works from any
  network, configured or not.
- **Seeding is inbound**: the peer who wants your file opens a connection **to you**. Two closed ends
  can never talk.

Public swarms hide a closed port: DHT and PEX hand out hundreds of peers, so there is always someone
open to dial. A private torrent carries the `private` flag, which **disables DHT, PEX and LSD**, so
the tracker's peer list is all there is, and an unreachable client uploads to almost nobody. Measured
here in August 2026: public trackers ratio 1.889, private 0.006, same client and week.

The cause then was CGNAT: the router's WAN address was in `100.64.0.0/10`, shared with other
customers, so a correct port forward received nothing. The line has had a public IPv4 since October
2026 (the ISP's add-on), and the forward works. If it ever stops: compare the router's WAN address
(over UPnP, `GetExternalIPAddress`) with the public one, since the ISP could revert it.

The alternatives, if the home line cannot take inbound:

| Option | What it gives | Notes |
| :--- | :--- | :--- |
| Port forward at home | ratio, with the home address in every swarm | needs a public IPv4 |
| qBittorrent inside gluetun, on a VPN that forwards a port | ratio, and the ISP stops seeing torrent traffic | Proton and PIA forward automatically in gluetun, AirVPN by hand; **NordVPN does not forward ports**, by policy. The arrs would then talk to `gluetun:8080` |
| Seedbox | ratio, completely; home never appears in a swarm | costs more |

A SOCKS5 proxy cannot fix inbound: accepting connections through one needs SOCKS5 `BIND`, which no
commercial provider allows. A proxy hides the address; it does not make the box reachable. The real
exposure of seeding from home is the address sitting in public swarms, which is the argument for a
VPN, not an open port: the peer protocol has no login, shell or file access.

```bash
# the port, from outside the network
nc -vz <public-ip> <port>

# incoming peers: an inbound peer carries the I flag. Status says "connected" either way
docker exec qbittorrent curl -s \
  "http://localhost:8080/api/v2/sync/torrentPeers?hash=<hash>&rid=0" | grep -o '"flags":"[^"]*"'

# a shareable speedtest, which trackers ask for
docker exec speedtest-tracker speedtest --accept-license --accept-gdpr -f json
```

All flags without an `I` after a few minutes of seeding means the chain is broken: check the three
places the port must agree ([media-policy.md](media-policy.md#the-bittorrent-port)).

## Indexers

Eight public (1337x, The Pirate Bay, YTS, LimeTorrents, Knaben, NoNaMe Club, BigFANGroup, and Nyaa.si
for anime) and four private (TorrentLeech, DigitalCore, C411, retrotoon.world).

Prowlarr's indexers live only in `appdata`: every one holds a passkey or an API key, and this repo is
public. A backup of all of them, credentials included, is written to
`appdata/prowlarr/indexers-backup.json`, and the nightly archive has the database.

- **Removed** because they could not work: Elitetorrent and MoviesDVDR (definition dropped upstream,
  server unreachable), RoTorrent (API 404), and Frozen Layer (definition deleted upstream, so
  Prowlarr raised an error-level health issue on every start, which its Telegram connection
  forwarded). A missing definition alone is not a reason to delete: Prowlarr keeps running one it
  already loaded, but it can never be re-added.
- **LimeTorrents is in Sonarr but not Radarr**, and forcing a sync will not fix it. Radarr validates
  an indexer by running an **empty** query in its categories; LimeTorrents returns nothing for an
  empty movie query, so Radarr refuses it (`400 No Results in configured categories`) even with
  `forceSave=true`.
- An indexer that is down on match nights is the line, not the tracker
  ([alerting.md](alerting.md#an-indexer-down-on-a-match-night-is-the-line-not-the-tracker)).
