# Private trackers

One page for the whole subject: the policy that decides what gets deleted and when, the recipe for
setting a tracker up on this box, and then each site with its own rules and the configuration applied
for it.

## My policy

The rule of record. Everything else in this file is how it is enforced.

```
PUBLIC tracker      stop seeding the moment the library has the file.
                    delete the film as soon as I have watched it. Nothing is owed.

PRIVATE tracker     the tracker's rules come first, always.
                    watched, rules met      -> delete it.
                    watched, rules NOT met  -> it stays, and it goes the moment they are met.
```

| Situation | What happens | Who does it |
| :--- | :--- | :--- |
| Public, imported into the library | torrent and its download-side name removed at once | `seed-cleanup.py` |
| Public, watched | film deleted | Maintainerr, two days after watching |
| Private, still in the library | keeps seeding: the library shares the same bytes, so it costs no disk | nothing to do |
| Private, watched, goal met | torrent and data removed on the next hourly pass | `seed-cleanup.py` |
| Private, watched, goal pending | kept and tagged `waiting-seed`, rechecked hourly, deleted the hour it clears | `seed-cleanup.py` |
| Private, owing a hit & run | never deleted, whatever else is true | `seed-cleanup.py` |

Why the split: seeding a public torrent buys nothing. There is no account and no ratio requirement,
so the only thing it produces is this address sitting in a public swarm for weeks. On a private
tracker seeding **is** the currency, and deleting early is the most expensive mistake available,
because an unpaid torrent turns into a hit & run and three of those disable an account.

**Nothing is ever deleted while a tracker is still owed.** That is the one line in this file with no
exceptions.

Where it lives: goals per tracker in `config/qbittorrent/seed-rules.json`, enforcement in
`scripts/seed-cleanup.py`, the film side in Maintainerr, and the full lifecycle in the README's
deletion policy.

## Three numbers, before touching anything

**Ratio is the tracker's number, not the client's.** They are not the same and the difference is not
small. Measured on 2026-08-20: DigitalCore had **211.64 GiB really downloaded but only 11.68 GiB
counted** (freeleech) and **7.25 GiB really uploaded but 32.25 GiB credited** (upload multipliers),
an on-site ratio of 2.76 while qBittorrent showed 0.006. Always read the site's own profile page.

**The buffer, not the ratio, is what governs a decision.** How many GB can still be downloaded before
crossing the line the site disables accounts under:

```
buffer   = uploaded - min_ratio x downloaded
headroom = buffer / min_ratio        <- GB of non-freeleech downloads that still fit

TorrentLeech, 2026-08-21:  31.17 - 0.4 x 59.84 = 7.23 GB buffer  ->  18 GB of headroom
```

Freeleech never moves `downloaded`, so it can only ever help. That is why "freeleech only" is the
standard remedy for a thin ratio, and why the grabber can keep running while the *arrs are held back.

**Hit & run is a separate way to lose the account.** It is per torrent: take one and fail to seed it
back for the required time or ratio and you collect a warning regardless of your overall ratio. The
site counts seeding time from announces, so a paused, errored or file-missing torrent pays nothing
off, which is why the alert here is not "how many obligations exist" but "how many are owed with the
clock stopped".

## How a tracker is set up on this box

Six places, in the order they matter. Configure them in this order for a new site.

**1. Prowlarr, which is where filtering belongs.** Prowlarr full-syncs its indexers to Radarr and
Sonarr, so anything switched in an *arr gets overwritten on the next sync: the durable place is
Prowlarr. Most Cardigann definitions carry a **`Search freeleech only`** checkbox, which appends the
site's own freeleech facet to every query, and every app searching through Prowlarr inherits it.
Check for it with:

```bash
docker exec prowlarr sh -c 'sed -n "/^settings:/,/^login:/p" /config/Definitions/<site>.yml'
```

**2. Radarr's `requiredFlags`, as a second belt.** `requiredFlags = [1]` is `G_Freeleech` and it
survives the Prowlarr sync. **Sonarr has no such field** (it exists only on Radarr's Torznab
indexer), which is precisely why step 1 is the real control and this one is only a backstop. The API
rejects the save with a 400 while the indexer is unreachable; `?forceSave=true` gets around it.

**3. Seed goals, per tracker host, in `config/qbittorrent/seed-rules.json`.** The goal is met by
hours **or** ratio, whichever arrives first. Set the hours above the site's own requirement: its
clock runs behind the client's, and a goal set exactly at the requirement came three hours from
deleting a torrent that still owed 88 hours.

```json
"trackers": {
  "tracker.torrentleech.org": { "min_seed_hours": 360, "min_ratio": 1.2 }
}
```

**4. `config/trackers/rules.json`**, which is what the automation reads: the site's minimum ratio,
its hit & run rule, the tracker hostnames as they appear in the announce URL, the free-space floor,
and the tiers that decide how hard the site is worked.

**5. autobrr, for the announce channel.** Freeleech is worth having the second it is announced, which
is minutes before an RSS poll sees it. One warning that matters: **Identification → Mechanism must
stay `None`**. SASL or NickServ makes the bot reconnect every one or two seconds and sites ban for
it. The bot nick is `<username>_bot`, it autojoins the announce channel, and the nick needs no
registering.

**6. Verify, because two of these fail silently.** After configuring:

```bash
# does the freeleech filter actually apply? every result should carry the flag
curl -s -H "X-Api-Key: $KEY" \
  "http://localhost:9696/api/v1/search?query=1080p&indexerIds=<id>&type=search" \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); \
    print(len(d), sum(1 for r in d if r.get("indexerFlags")))'

# what the site says about the account, and what the automation decided
scripts/tracker-stats.py && cat appdata/tracker-stats/state.json
DRY_RUN=1 scripts/tracker-control.py
```

`sync-arr-config.sh` used to report success while applying nothing; it is fixed, but the habit of
reading the value back rather than trusting the exit code is the reason it was found.

## The control loop

Since 2026-08-21 none of this depends on remembering to check a site. `tracker-stats.py` reads the
account every 30 minutes, `tracker-control.py` acts on it five minutes later, and the tier comes from
the headroom:

| Headroom | Prowlarr | Radarr | autobrr |
| :--- | :--- | :--- | :--- |
| under 25 GB | freeleech results only | `requiredFlags = [1]` | 3 grabs a day |
| 25 to 100 GB | freeleech results only | `requiredFlags = [1]` | 2 grabs a day |
| over 100 GB | all results | `requiredFlags = []` | 1 grab a day |

Nothing is written when the value already matches, so this is silent except at a crossing, and it
refuses to act on a reading over three hours old. Every change goes to Telegram.

Two properties worth knowing:

- **The grab rate is a disk budget, not a preference.** A grab cannot be deleted until its hit & run
  window closes, so the disk cost is the rate times the retention: 2 a day at ~20 GB over a 15-day
  goal holds ~600 GB. Below `min_free_gb` the grabber is disabled outright whatever the ratio says,
  with 50 GB of hysteresis so it does not flap while `seed-cleanup.py` frees torrents.
- **Credentials are not duplicated.** They are read from Prowlarr, which already holds them for the
  same site, and the session cookie is kept in `appdata/tracker-stats/` so the site sees about one
  login a day instead of 48.

Exported: `tracker_ratio`, `tracker_min_ratio`, `tracker_uploaded_bytes`,
`tracker_downloaded_bytes`, `tracker_buffer_bytes`, `tracker_headroom_bytes`, `tracker_points`,
`tracker_warning_seconds`, `tracker_hnr_pending`, `tracker_hnr_at_risk`,
`tracker_hnr_torrent_hours_left` per torrent, `tracker_tier_grabs_per_day`,
`tracker_tier_freeleech_only`, `tracker_grabber_paused_no_disk`. Four alerts sit on them: headroom
under 10 GB, ratio below the line, an obligation with the clock stopped, and no reading in two hours.
The last one matters most, because a stopped scrape looks exactly like a healthy account.

## TorrentLeech

### Its rules

Straight from staff in `#tlhelp`, 2026-08-20:

| Rule | Value |
| :--- | :--- |
| Minimum ratio | **0.4.** Below it the account is warned, seven days to fix it, then disabled |
| Hit & run | 240 h of seeding **or** ratio 1:1 per torrent. **Three uncleared = disabled** |
| Freeleech | does not count towards `downloaded`. The only safe thing to grab on a thin ratio |
| Download slots | one at the entry class, and a partially downloaded torrent occupies it |
| Bonus points | 1 point = 14.3 MB of upload credit, paid per GB held per hour |
| On being disabled | **the passkey is reset**, so every torrent in the client needs its announce URL updated |
| Support | IRC `#tlhelp`. They ask for the username, the account email in private, and a speedtest link |

### How it is configured here

| Piece | Value | Why |
| :--- | :--- | :--- |
| Prowlarr | `Search freeleech only` driven by the tier | one switch every app inherits, and it survives the sync |
| Radarr | `requiredFlags = [1]` | second belt, and the only *arr that has the field |
| Sonarr | indexer left **on** at every tier | the site has series, and Prowlarr is already filtering |
| Seed goal | 360 h or ratio 1.2 | above the 240 h obligation, because their clock lags the client's |
| autobrr | `TL freeleech ratio builder`: freeleech, 5 to 40 GB, 2 to 3 a day, no category, tag `ratio` | early seeding is where upload comes from |
| Free-space floor | 400 GB | each grab is locked for its H&R window |

**No category on the grabber's action is deliberate.** A category makes Radarr adopt a download and
then complain it cannot import it. These grabs belong to nothing: they exist to be uploaded from, and
the `ratio` tag says so. `seed-cleanup.py` finds no library copy for them and falls through to the
TorrentLeech goal, which is past the obligation either way.

Two API traps, both of which cost an hour: in a qBittorrent action `label` does nothing (it is
Deluge's field, tags come from `tags`), and `POST /api/filters` fails with
`NOT NULL constraint failed: filter.resolutions` unless every list field is present, even empty,
while storing neither the actions nor the indexer link, which need `POST /api/actions` and a
follow-up `PUT /api/filters/{id}` with nulls stripped.

### Links

- Everything about warnings: <http://wiki.torrentleech.org/doku.php/everything_about_warnings>
- Common mistakes: <https://wiki.torrentleech.org/doku.php/common_mistakes>
- Guide for newly invited users: <https://wiki.torrentleech.org/doku.php/newly_invited_users/>
- Their autobrr page: <https://wiki.torrentleech.org/doku.php/autobrr>
- How to maintain your ratio: <https://forums.torrentleech.org/t/how-to-maintain-your-ratio/78082/>
- **Freeleech added in the last 10 minutes**, the one worth bookmarking:
  <https://www.torrentleech.org/torrents/browse/index/facets/tags%3AFREELEECH_added%3A%255BNOW%252FMINUTE-10MINUTES%2520TO%2520NOW%252FMINUTE%252B1MINUTE%255D>

## DigitalCore

### What is known

- **Freeleech periods and upload multipliers** are generous enough that the on-site ratio looks
  nothing like the client's: 2.76 on site against 0.006 in qBittorrent, same week.
- **Bonus points** accumulate and are spendable; read the shop before grinding ratio by hand.
- The per-torrent **`Connectable`** column under *Activity & Logs → Transfers* is the best inbound
  check available anywhere: a third party telling you whether the world can reach your port. It read
  `No` on all seven torrents behind CGNAT and flipped to `Yes` minutes after a public IP arrived.
- Its API key, the one Prowlarr uses for searching, returns **403 on every user endpoint**
  (`/api/v1/user`, `/users/current`, `/account`, `/user/stats`, `/me`), so reading the account needs a
  browser session like TorrentLeech's.

### Still open

Minimum ratio, its hit & run rule, seed time per torrent, slots per class, and how freeleech is
flagged. Until those are known it runs on the generic goal (240 h or ratio 1.0) and has no entry in
`config/trackers/rules.json`.

## C411, BTSCHOOL, retrotoon.world

Unknown, which is the state that loses accounts. For each, six answers are needed before its
configuration can be written honestly rather than guessed:

1. minimum ratio, and what happens below it;
2. what triggers a hit & run and how it clears;
3. any minimum seed time per torrent;
4. how many download slots the current class allows;
5. whether freeleech exists and how it is flagged (a Cardigann `freeleech` setting, an indexer flag,
   or nothing);
6. what the bonus-point shop sells.

C411 is currently down in Prowlarr, which is its own thing to fix first.

## Adding a new private tracker

1. Read its rules pages and fill in the six answers above.
2. Add the indexer in Prowlarr, check the definition for a `freeleech` setting, and test a search.
3. Add its hostname and rules to `config/trackers/rules.json`, and its seed goal to
   `config/qbittorrent/seed-rules.json`, hours above what the site asks for.
4. If it has an announce channel, add it to autobrr with Mechanism `None`, and size the grab rate as
   a disk budget rather than a preference.
5. Run `tracker-stats.py` and confirm the site's numbers come back; then `DRY_RUN=1
   tracker-control.py` and confirm the tier it picks is the one you expect.

## Where upload actually comes from

Useful when deciding what to grab, because being reachable is necessary and not sufficient.

You only upload when somebody else wants the same torrent **and picks you**, which leaves two
strategies:

1. **Be early.** A release minutes old has hundreds of leechers and a handful of seeders, so
   everyone asks you. This is the reliable one, and the entire reason autobrr sits on an IRC channel.
2. **Be the only one.** A large niche torrent nobody else seeds means everyone who ever wants it
   takes the whole thing from you. Measured 2026-08-20: a 73.8 GB freeleech disc set with 1 seeder
   and 33 leechers beats a popular 5 GB release with 50 seeders competing for the same demand.

What does not work is seeding an old catalogue: 16 torrents with 20 to 259 seeders each and 18
leechers between all of them produced 0 KB/s over a day.

## Verifying inbound, the only measurement that matters

Seeding needs inbound connections and downloading does not, which is why a broken setup looks fine
until the ratio collapses. A private torrent carries the `private` flag, which disables DHT, PEX and
LSD, so the tracker's peer list is the only source and an unreachable client uploads to nobody.
Measured here, same client and week: public trackers 397.6 GB up against 210.5 GB down (ratio 1.889),
private trackers 3.6 GB up against 573.1 GB down (0.006). The CGNAT diagnosis behind that is in
[seeding-and-ratio.md](seeding-and-ratio.md).

```bash
# the port, from outside the network entirely
nc -vz <public-ip> <port>

# incoming peers: an inbound peer carries the I flag
docker exec qbittorrent curl -s \
  "http://localhost:8080/api/v2/sync/torrentPeers?hash=<hash>&rid=0" | grep -o '"flags":"[^"]*"'

# a shareable speedtest, which trackers ask for
docker exec speedtest-tracker speedtest --accept-license --accept-gdpr -f json
```
