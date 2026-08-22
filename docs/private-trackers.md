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
| Public, imported into the library | torrent and its download-side name removed at once | `scripts/trackers/seed-cleanup.py` |
| Public, watched | film deleted | Maintainerr, two days after watching |
| Private, still in the library | keeps seeding: the library shares the same bytes, so it costs no disk | nothing to do |
| Private, watched, goal met | torrent and data removed on the next hourly pass | `scripts/trackers/seed-cleanup.py` |
| Private, watched, goal pending | kept and tagged `waiting-seed`, rechecked hourly, deleted the hour it clears | `scripts/trackers/seed-cleanup.py` |
| Private, owing a hit & run | never deleted, whatever else is true | `scripts/trackers/seed-cleanup.py` |

Why the split: seeding a public torrent buys nothing. There is no account and no ratio requirement,
so the only thing it produces is this address sitting in a public swarm for weeks. On a private
tracker seeding **is** the currency, and deleting early is the most expensive mistake available,
because an unpaid torrent turns into a hit & run and three of those disable an account.

**Nothing is ever deleted while a tracker is still owed.** That is the one line in this file with no
exceptions.

**What each tracker pays for is not the same thing.** The rules decide the economics, so the
deletion rule reads them rather than applying one number everywhere:

| Tracker | What actually pays | When a torrent may go |
| :--- | :--- | :--- |
| TorrentLeech | upload only: no bonus for holding anything | hit & run cleared at 240 h or ratio 1:1, then as soon as it goes quiet |
| DigitalCore | upload, plus a little for holding (0.5 p/hour/torrent, 1% off downloads per 10 GB) | 120 h or 1:1, so it rotates twice as fast as TorrentLeech |
| C411 | upload only, with the ratio wall 2 GB away | their hit & run is disabled site-wide, so: as soon as it goes quiet |
| BTSCHOOL | **everything pays double upload one month after release**, so holding a month multiplies every byte uploaded after it | not worth judging before that month is up |
| retrotoon | points for seeding, and no ratio rule at all | their 72 h, then as soon as it goes quiet |

The floor for "quiet" is `min_upload_gb_per_day` in `config/qbittorrent/seed-rules.json`, per tracker.
A torrent with less than 12 hours of upload history is never judged on it, because a release added an
hour ago has produced no evidence either way.

**The manual override is a tag.** Add `keep` to a torrent in qBittorrent and `scripts/trackers/seed-cleanup.py` will
never touch it, whatever the goals say. It needs no deploy and no config change, which is the point:
it exists for the moment something is about to be removed and the answer is "not yet, explain it
first".

Where it lives: goals per tracker in `config/qbittorrent/seed-rules.json`, enforcement in
`scripts/trackers/seed-cleanup.py`, the film side in Maintainerr, and the full lifecycle in the README's
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
scripts/trackers/stats.py && cat appdata/tracker-stats/state.json
DRY_RUN=1 scripts/trackers/control.py
```

`scripts/sync/arr-config.sh` used to report success while applying nothing; it is fixed, but the habit of
reading the value back rather than trusting the exit code is the reason it was found.

## The control loop

Since 2026-08-21 none of this depends on remembering to check a site. `scripts/trackers/stats.py` reads the
account every 30 minutes, `scripts/trackers/control.py` acts on it five minutes later, and the tier comes from
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
  with 50 GB of hysteresis so it does not flap while `scripts/trackers/seed-cleanup.py` frees torrents.
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
the `ratio` tag says so. `scripts/trackers/seed-cleanup.py` finds no library copy for them and falls through to the
TorrentLeech goal, which is past the obligation either way.

Three API traps in autobrr, each of which cost an hour:

- In a qBittorrent action **`label` does nothing**. It is Deluge's field; qBittorrent tags come from
  `tags`. A filter can match, push successfully, and still land untagged.
- **`POST /api/filters` fails** with `NOT NULL constraint failed: filter.resolutions` unless every
  list field is present, even empty, and stores neither the actions nor the indexer link: those need
  `POST /api/actions` and a follow-up `PUT /api/filters/{id}` with nulls stripped.
- **`PUT /api/actions/{id}` detaches the action** unless the body carries `filter_id`, because the
  GET that feeds the update does not return that field. A detached action leaves the filter matching
  releases and pushing nothing, and the only trace is one line in autobrr's log:
  `no active actions found for filter`. It cost four freeleech releases on 2026-08-21, announced in
  the hour after the action was edited. `scripts/trackers/control.py` now asserts
  `actions_enabled_count >= 1` on every run, and the "filter matched" count in autobrr's own log is
  worth comparing against what actually reached the client.

**One download slot means one download.** TorrentLeech's entry class grants a single slot and a
partially downloaded torrent occupies it, so qBittorrent's queue is set to `max_active_downloads: 1`
in `config/qbittorrent/preferences.json`. Grabs then queue instead of jamming the slot, which is also
why the grab rate can be raised without thinking about concurrency.

### Links

- Everything about warnings: <http://wiki.torrentleech.org/doku.php/everything_about_warnings>
- Common mistakes: <https://wiki.torrentleech.org/doku.php/common_mistakes>
- Guide for newly invited users: <https://wiki.torrentleech.org/doku.php/newly_invited_users/>
- Their autobrr page: <https://wiki.torrentleech.org/doku.php/autobrr>
- How to maintain your ratio: <https://forums.torrentleech.org/t/how-to-maintain-your-ratio/78082/>
- **Freeleech added in the last 10 minutes**, the one worth bookmarking:
  <https://www.torrentleech.org/torrents/browse/index/facets/tags%3AFREELEECH_added%3A%255BNOW%252FMINUTE-10MINUTES%2520TO%2520NOW%252FMINUTE%252B1MINUTE%255D>

## DigitalCore

Rules and FAQ read 2026-08-21. The healthiest account here: ratio **4.21**, 49.19 GiB up against
11.68 GiB down, 711 bonus points, **0 hit and runs**.

### Its rules

| Rule | Value |
| :--- | :--- |
| Minimum ratio | **0.5**. Below it, five days of *ratio watch*, then **leeching is revoked**. The account stays enabled and leeching returns when the ratio does |
| Hit & run | the obligation starts at **10% downloaded** and clears at **5 days of seeding or ratio 1:1**. Freeleech is **not** exempt |
| How it is detected | **one hour without an announce** and the torrent is listed. You then have 10 days to fix it before it becomes permanent and needs points, upload credit or a donation |
| H&R penalty | **5 of them is a warning**, more is a download ban |
| Whose clock counts | **the site's, and only the site's.** Its FAQ says outright that what the client shows does not matter, because seed time is logged only when an announce succeeds |
| Free automatically | every new torrent for **24 h** (the download must finish inside the window), and **anything 15 GB or larger** |
| Leech bonus | **10 GB actively seeded = 1% off what a download costs**, averaged over 7 days. 1 TB seeded is 100%, which is a site-wide freeleech |
| Banned clients | uTorrent (except v2.2.1), BitComet, Azureus, Shareaza, BitLord, ktorrent and others. **qBittorrent is not on the list** |
| Inactivity | 90 days without signing in disables the account |
| Cross-seeding | **explicitly allowed**, in those words |
| Modifying their torrents | **forbidden**: no extra trackers, no DHT, no PEX, no re-hosting through debrid services |
| VPN | allowed, and the site is hosted in Russia where VPN IPs are blocked, hence their own proxy at `prxy.digitalcore.club` |
| Supported tools | Prowlarr, **autobrr**, Jackett, Irssi AutoDL, by name |

Their displayed **buffer is measured at ratio 1.0**, not at the 0.5 that decides anything:
`49.19 - 11.68 = 37.51 GiB` is what the header shows, while the real headroom is
`(49.19 - 0.5 x 11.68) / 0.5 = 86.7 GB` of paid downloads.

### The leech bonus is the best mechanic on any tracker here

Worth reading twice, because it inverts the usual problem. Ratio normally needs somebody to want
your files; this pays for **holding** them:

```
10 GB seeded            = 1% off every download
272 GB seeded (now)     = 12%, which is what the account shows
1000 GB seeded          = 100%, i.e. everything is freeleech, site-wide
```

Two details decide how to play it:

- **Only 50 GiB per torrent counts.** A 200 GB torrent contributes 50. So many medium torrents beat
  a few enormous ones, which is the opposite of the TorrentLeech strategy.
- **Scarcity pays double.** The bonus is scaled by `1 + (1 / seeders)`, so a torrent with a single
  seeder counts twice as much as a well-seeded one. Being the only seeder of something obscure is
  worth more here than being early on something popular.

Add the automatic freeleech on anything 15 GB or larger, and DigitalCore is the cheapest ratio
available on this box: seed a terabyte of scarce medium-sized torrents and downloads stop costing
anything at all.

### How it is configured here

| Piece | Value | Why |
| :--- | :--- | :--- |
| Prowlarr | API key, FlareSolverr, no freeleech switch | the definition is an API one, so there is no `freeleech` checkbox to flip |
| Seed goal | **168 h / ratio 1.0**, keyed on both announce hosts | a week, 40% above their 5 days, because the site counts only what it saw announced. It was 240 h while their rule was unknown |
| H&R measurement | 120 h / 1.0, their real rule | 8 obligations open right now, worst 115 h to go. None with the clock stopped |
| cross-seed | **included**, and the first two matches came from here | the site says cross-seeding is fine, and it is free ratio |
| Spare disk | **up to 700 GB held for the leech bonus**, floor 600 GB free | see below: here, not deleting is the correct policy |
| Rules of record | `config/trackers/rules.json` | including the two things never to do to their torrents |

#### Why the leech bonus is NOT bought with disk

The obvious move is to stop deleting on this tracker and let the bonus grow. Measured on 2026-08-21,
that move costs more than it returns:

```
94 GB  already shares its inodes with the library  ->  ~9% bonus, free, forever
179 GB separate copies (RAR archive sets, films already watched)  ->  ~18% bonus, paid in disk
```

So a 27% bonus is nine points free and eighteen points bought with 179 GB. And the thing it buys is
cheaper downloading on the one tracker already at **ratio 4.21 with 86 GB of headroom**: a discount on
a problem that does not exist. The same 179 GB is what the TorrentLeech freeleech grabber needs, on
the account that actually has a deadline. One film accounted for 130 GB of it, Project Hail Mary held
twice over.

**So the bonus is grown only through hardlinks**: the library copies, which seed for free whether
anyone plans it or not, and cross-seeds, which are hardlinks by construction. Both cost nothing and
neither needs a policy.

`bonus_hold` stays implemented in `scripts/trackers/control.py` and unconfigured, for the day this tracker is
leeched from heavily enough for the arithmetic to reverse. What it would do, and its ranking
`min(size, 50 GiB) x (1 + 1/seeders)`, is in that function's docstring.

**The passkey trick used on TorrentLeech is forbidden here.** When TorrentLeech reset its passkey,
every torrent was fixed by rewriting its announce URL with `addTrackers` and `removeTrackers`. Rule 2
here forbids modifying their torrent files at all, so the same outage on this site means
re-downloading the `.torrent`, not editing it.

**Open obligations are not violations.** The metric here counts torrents that have not yet cleared
their 5 days, which is the normal state of anything recent. The site only *lists* a hit and run once
a torrent stops announcing for an hour, which is exactly what `tracker_hnr_at_risk` watches.

#### The grabber, as configured

Running since 2026-08-21, on their own announce channel:

| Piece | Value | Why |
| :--- | :--- | :--- |
| IRC | `irc.digitalcore.club:7000`, TLS, channel `#announce`, announcer `ENDOR` | they support autobrr by name |
| Nick | **`PlatanosVerdes`**, exactly the site username | their FAQ: the match is case sensitive, and it pays 0.4 points an hour for idling in `#digitalcore` |
| Bot mode | **on**, so the bot carries usermode `+B` | their IRC rules require a bot to identify itself |
| Auth | NickServ **empty**, invite through `ENDOR !invite <user> <irckey>` | their rules say a bot authenticates with the site username and IRC key, not NickServ |
| Filter | `DC freeleech ratio builder`: freeleech, 15 to 30 GB, **1 a day** | see the arithmetic below |
| Action | qBittorrent, no category, tag `ratio` | same as TorrentLeech: these belong to no *arr |

**Why 1 a day and 15 to 30 GB.** Their hit & run window is 5 days rather than TorrentLeech's ten, so
a grab here is locked for less than half as long, and the seed goal of 168 h is what actually holds
it: `1 x ~22 GB x 7 days = ~150 GB` of steady-state disk. The floor of 15 GB is not a preference
either: **anything 15 GB or larger is automatically freeleech here**, so it is the size at which a
grab is guaranteed to cost nothing against ratio. The ceiling keeps one release from eating a week of
the budget, and it also sits under the 50 GiB the leech bonus counts per torrent.

Worth saying plainly: this account does not need the ratio. It sits at 4.21 with 86 GB of headroom,
so the grabber here buys upload and bonus points rather than solving a problem, and it costs ~150 GB
of disk that TorrentLeech's grabber could use. At 1 a day that trade is cheap enough to be worth it;
if disk gets tight, this is the first thing to turn off, not the last.

#### Four API traps, if this is ever rebuilt

- `POST /api/indexer` wants `settings` as an **object**, not a list: a list returns
  `cannot unmarshal array into Go struct field Indexer.settings`.
- Creating the indexer does **not** create its IRC network. That is a separate `POST /api/irc`.
- A channel created that way arrives **disabled**, and a disabled channel is never joined.
- The network is updated at `PUT /api/irc/network/{id}`, not `/api/irc/{id}`, which answers 404.

### Still open

Nothing about the rules or the tooling. The account still cannot be read automatically, because a
normal API key reaches only the torrent endpoints and not profile data, so its ratio and bonus have
to be read by eye.

## retrotoon.world

Rules read 2026-08-21. Nothing downloaded yet: 0 up, 0 down, 273 bonus points.

### Its rules

| Rule | Value |
| :--- | :--- |
| Seed time | **72 h per torrent, reached within the first 10 days** after the download. The site calls this its strictest rule, "no exceptions" |
| Minimum ratio | none stated. The obligation here is time, not ratio |
| Right now | **site-wide freeleech for another 40 days** (until roughly 2026-09-30): nothing counts against ratio |
| Content | animation only, and strictly family-friendly. Adult content is an immediate permanent ban |
| Bonus | seed-bonus points, tripled on the daily featured torrent |

### How it is configured here

| Piece | Value | Why |
| :--- | :--- | :--- |
| Prowlarr | `Generic Torznab`, working | it is the generic definition, so there is no site-specific switch |
| Seed goal | **96 h / ratio 1.0**, keyed on `ann.retrotoon.world` | a third above their 72 h, which is margin for their clock without holding the disk for ten days |
| cross-seed | included | free ratio, same as everywhere |

Its announce host is **`ann.retrotoon.world`**, read out of a real `.torrent` fetched through
Prowlarr rather than guessed, so the seed goal is now theirs and not the generic default: **96 h**, a
third more than the 72 h they ask for, which covers a lagging tracker clock and still frees the disk
two and a half times sooner than 240 h did.

**autobrr is wired up and switched off.** The indexer, its passkey and the IRC connection to
`irc.retrotoon.world:6697` (`#announce`, nick `PlatanoVerde`, bot mode on) are all in place, and the
filter `RetroToon (off until wanted)` is **disabled**: the plumbing is done and the tap is closed,
because this is a cartoons-only site and disk is the scarce thing. Enabling it is one toggle.

**C411 cannot have a grabber at all**: autobrr ships no definition for it. That is no loss. With
1.95 GB of headroom the correct policy there is not to grab, and its hit & run system being disabled
does not change the ratio wall.

**The 40-day freeleech is the moment to take anything wanted from here**, since downloads cost
nothing against ratio while it lasts.

## BTSCHOOL

Rules read 2026-08-21, and this one has a deadline attached.

### The newbie assessment, which is nearly over

The account is inside its probation window with **2 days 15 hours left**, and to pass it needs
**50 GB uploaded, 50 GB downloaded and 6000 bonus points**. Current state: zero of all three.

That is not reachable in the time left by seeding, and the site says so itself: the other way through
is a donation. So this is a decision, not a task: donate, or let the account go. Recorded in
[../PENDING.md](../PENDING.md).

### Its rules

| Rule | Value |
| :--- | :--- |
| Hit & run | **20 h of seeding within 10 days** of finishing, whatever the torrent's size. Upload above download clears it outright |
| H&R penalty | **10 unmet obligations is a ban**. Clearing one costs 20000 bonus points, and an H&R ban is lifted only by a 100 CNY donation |
| Minimum ratio | not on the Rules page: it says a low ratio costs download privileges and puts the number in the FAQ |
| Account pruning | no traffic at all for 30 days without logging in deletes the account; 150 days deletes an unsealed one |
| Upload speed | **their cap, 25 MB/s**. Above it, reported upload is penalised threefold; above 100 MB/s the account is banned automatically |
| Promotions | Free, 2X, 2XFree, 50%, 30%. Anything over 20 GB is automatically Free, so are Blu-ray and HD DVD raw discs and the first episode of a season, and **everything becomes permanent 2X upload one month after release** |
| Their torrents | do not upload their `.torrent` files to other trackers |
| Clients | only clients on their whitelist, which is in the FAQ |

### How it is configured here

| Piece | Value | Why |
| :--- | :--- | :--- |
| Prowlarr | id 11, FlareSolverr, **search works, download does not** | the site answers a `.torrent` request with an invalid file, which is what an account still inside its newbie assessment looks like. Its announce host is therefore still unknown |
| Seed goal | the generic 240 h / ratio 1.0 | twelve times their 20 h requirement, so it cannot cause an H&R |
| cross-seed | included | |
| Upload cap | **not applied** | qBittorrent's limit is global and cannot be set per tracker, so capping at 25 MB/s would throttle TorrentLeech, which is where the ratio comes from. Nothing seeds here yet and the measured peak across all trackers is about 0.5 MB/s, so it is theoretical until it is not |

The promotion rules are the most generous of any site here and are worth reading twice: **over 20 GB
is free, and after a month everything pays double upload**. If the account survives its probation,
that combination is the cheapest ratio available anywhere on this box.

## C411

Rules read 2026-08-21. **This is the tightest account on the box, and it is the one nobody was
watching.** The site shows 52.2 GB up against 63.3 GB down for a ratio of **0.83**, and its minimum
to download anything is **0.8**.

```
headroom = (52.2 - 0.8 x 63.3) / 0.8 = 1.95 GB of paid downloads left
```

Two gigabytes. One ordinary film that is not freeleech blocks leeching on this site.

And the ratio is thinner than it looks: **new accounts get 50 GB of upload credit** that counts
towards the ratio, so of those 52.2 GB only **2.3 GB were really uploaded** (which matches
qBittorrent exactly). The credit is a one-off, it does not grow back, and it is what has been holding
this account above its line.

### Its rules

| Rule | Value |
| :--- | :--- |
| Minimum ratio to download | **0.8**. Below it, downloads are blocked until it recovers. The account is not disabled |
| Signup credit | **50 GB of upload**, counted in the ratio |
| Hit & run | **currently disabled site-wide.** Only ratio decides whether you can leech |
| H&R when it returns | 72 h **or** ratio 1.0 per torrent, with a 24 h grace period before the clock starts |
| H&R exemptions | global ratio ≥ 2.0, your first three torrents, anything under 100 MB, anything less than half downloaded |
| H&R sanctions | a warning 24 h before the deadline, 3 active violations block downloads, **5 strikes is an automatic ban** |
| Cross-seeding | **explicitly allowed**: "vous uploadez réellement les données" |
| Freeleech | full (0x), 50%, and 2x upload, per torrent, per account or site-wide |
| Cheating | announced upload is cross-checked against real swarm activity; ghost leech and modified clients lead to a permanent ban |

### How it is configured here

| Piece | Value | Why |
| :--- | :--- | :--- |
| Prowlarr | id 9, API key set, **proxy removed on 2026-08-21** | two separate faults were hiding behind one symptom: the SOCKS credentials had expired, and the site itself is now serving a **Maintenance** page, which is what Prowlarr cannot parse as XML. The announce path is fine throughout, which is why 4 torrents keep seeding |
| Seed goal | the generic 240 h / ratio 1.0 | more than three times their 72 h, so nothing can trip their H&R even after it comes back |
| H&R measurement | on, as if their system were enabled | it is documented as returning, and measuring early costs nothing |
| cross-seed | included | |

### From its terms of use, read 2026-08-21

| Term | What it means here |
| :--- | :--- |
| **Never share your passkey or personal announce link** | checked: nothing in this repo contains one. The only long hex strings tracked are an acestream content id and Docker image digests |
| VPN and proxies | not forbidden; using one **to cheat the ratio** is. C411's Prowlarr entry routes its searches through the `nordvpn` SOCKS proxy, which is search traffic and touches no announce |
| One account per person, no sharing | |
| Uploads | must be seeded at least 48 h after publishing, if anything is ever uploaded here |
| Sanctions | progressive: warning, then temporary suspension, then permanent ban. Not the instant disable TorrentLeech uses |
| New members | get a grace period, which is what the 50 GB credit is |

### What to do about it

Nothing automatic can help here yet, because the account cannot be read: its Prowlarr entry holds no
username and password, so `scripts/trackers/stats.py` has no way in. Until it does, the rule is manual and
simple: **on C411, freeleech only.** Everything else there is two gigabytes from blocking the
account's downloads.

## Adding a new private tracker

One site per sitting, and the section it earns on this page looks like TorrentLeech's above: rules,
then the configuration applied, then why.

1. **Read its rules pages** and answer the six questions above. Write them down here before
   configuring anything; a guessed seed goal is how accounts are lost.
2. **Check the indexer actually works before believing anything else.** A dead proxy, a missing
   definition file and a disabled account look identical from outside:

   ```bash
   K=$(sed -n 's/.*<ApiKey>\([^<]*\)<.*/\1/p' appdata/prowlarr/config.xml | head -1)
   curl -s -H "X-Api-Key: $K" http://localhost:9696/api/v1/indexer/<id> > /tmp/ix.json
   curl -s -X POST -H "X-Api-Key: $K" -H 'Content-Type: application/json' \
        --data-binary @/tmp/ix.json http://localhost:9696/api/v1/indexer/test   # the real error
   curl -s -H "X-Api-Key: $K" http://localhost:9696/api/v1/health               # how long it has failed
   ```

   C411 looked like a ban on 2026-08-21 and was a SOCKS proxy whose credentials had expired.
3. **Look for the freeleech switch** in its Cardigann definition and turn it on if the ratio is
   thin, since Prowlarr is the only place that covers every app.
4. **Add its hostname and rules to `config/trackers/rules.json`**, and its seed goal to
   `config/qbittorrent/seed-rules.json`, with the hours set above what the site asks for.
5. **If it has an announce channel**, add it to autobrr with Mechanism `None`, and size the grab
   rate as a disk budget rather than a preference.
6. **Add it to cross-seed** (`config/cross-seed/config.js`), because a tracker that has the release
   you already seed is free ratio.
7. **Verify, do not assume**: `scripts/trackers/stats.py` must come back with the site's own numbers, and
   `DRY_RUN=1 scripts/trackers/control.py` must pick the tier you expect.

## cross-seed, the free ratio

The same bytes, already on this disk, seeded on every private tracker that also has the release.
Matches are hardlinked, so a cross-seed costs an inode and nothing else, and the torrent starts at
100% with nothing to download.

What is configured, in `config/cross-seed/config.js`:

| Setting | Value | Why |
| :--- | :--- | :--- |
| `torznab` | the private indexers only | there is no account or ratio on a public tracker, so a cross-seed there buys nothing |
| `torrentDir` | qBittorrent's `BT_backup`, read-only | what the client already holds is the search list |
| `linkDirs` | `/data/downloads/cross-seed` | must be a path qBittorrent has mounted, and **cannot sit inside `dataDirs`**, which is why there is no `dataDirs` here |
| `duplicateCategories` | `true` | injects as `<category>.cross-seed`, so Radarr never sees a torrent it cannot import |
| `skipRecheck` | `false` | a flexible match can be a near miss, and seeding bad data is worse than not seeding |
| `searchCadence` | `1 day` | 30 s between queries, on trackers with request limits |

Two version facts worth keeping: cross-seed **6.13.2 cannot log into qBittorrent 5.2.3**, because
that version answers a successful login with `204` and the client code treats it as a failure. 6.13.7
handles it. And `excludeOlder` takes vercel `ms` strings, so `180d` works and `6 months` does not.

The consequence for deletion is real and deliberate: a cross-seeded file has a link count above one
while the second torrent exists, so `scripts/trackers/seed-cleanup.py` reads it as "still in the library" and keeps
both. Bytes that used to be reclaimed after watching now wait for the cross-seed to finish paying its
own tracker. That is the price of free ratio, and it is one more reason the deleting side belongs in
qbit_manage, which understands cross-seeds explicitly.

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
