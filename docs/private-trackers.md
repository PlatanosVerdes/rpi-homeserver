# Private Trackers

Written on 2026-08-20, the day a TorrentLeech account was disabled for ratio and the cause turned
out to be five months of an unreachable port. Everything here is either measured on this setup or
came straight from tracker staff.

## The one rule that governs all of it

**Seeding needs inbound connections. Downloading does not.** Downloading is outbound: you dial the
peer who has the file, and that works from any network with nothing configured. Seeding is inbound:
the peer who wants your file has to reach **you**. Two closed ends can never talk.

Public swarms hide a closed port, because DHT and PEX hand out hundreds of peers to dial out to. A
private tracker's torrents carry the `private` flag, which **disables DHT, PEX and LSD**, so the
tracker's peer list is the only source and an unreachable client can only upload to the few peers
it manages to call first. Measured here on 2026-08-20, same client, same week:

| | Uploaded | Downloaded | Ratio |
| :--- | ---: | ---: | ---: |
| Public trackers | 397.6 GB | 210.5 GB | **1.889** |
| Private trackers | 3.6 GB | 573.1 GB | **0.006** |

That gap is the entire story. See [seeding-and-ratio.md](seeding-and-ratio.md) for the CGNAT
diagnosis and [../SYSTEM_NOTES.md](../SYSTEM_NOTES.md) section 10 for the port chain.

## The accounting that matters is the tracker's, not the client's

qBittorrent's numbers describe reality; the tracker's numbers decide your fate, and they are not the
same thing. On DigitalCore on the same day: **211.64 GiB really downloaded but only 11.68 GiB
counted** (freeleech), and **7.25 GiB really uploaded but 32.25 GiB credited** (upload multipliers),
for an on-site ratio of **2.76** while the client showed 0.006. The reverse also happens:
TorrentLeech counted 59.84 GB down against 15.25 GB up (0.255) out of 364.8 GB actually held.

**Always read the ratio on the tracker's own profile page before concluding anything.**

## TorrentLeech

Rules and mechanics, from staff in `#tlhelp` on 2026-08-20:

- **Minimum ratio for membership: 0.4.** Below it, the account is disabled. Warned at 0.351 on
  08-10, disabled at 0.255 on 08-16.
- **Three uncleared hit & run warnings also disable the account.**
- **Freeleech torrents do not count against ratio.** While a ratio is unhealthy, freeleech is the
  only safe thing to grab, and it is the fastest way to build upload.
- **One download slot.** Downloading several torrents at once jams the slot and nothing progresses.
  And **every partially downloaded torrent occupies a slot**, so stick to one at a time.
- **Being disabled resets the passkey.** Every torrent already in the client then announces
  `unregistered torrent pass` and must have its announce URL updated, or the `.torrent` re-downloaded.
- Support is their IRC channel `#tlhelp`. They ask for the site username, and to get you seeding
  again they ask for the account email (send that in a private message, not in the channel) and a
  speedtest link.

What worked in that conversation, for next time: answer the question asked, own the mistake without
re-explaining the technical cause once they push back, use **their** numbers rather than yours
("I need about 45 GB of upload to reach 1.0"), and bring a control already applied rather than a
promise.

### State after the 2026-08-20 reinstatement

The account was re-enabled by staff on 2026-08-20 with two conditions, and one deadline that is
easy to forget:

| | |
| :--- | :--- |
| **Deadline** | **14 days to fix the ratio, i.e. 2026-09-03** |
| Condition | freeleech only until the ratio is healthy, their words: otherwise "you'd be disabled again, also quickly" |
| Condition | one torrent at a time, since a partially downloaded one occupies the single slot |

The arithmetic that matters, because the threshold is **0.4 and not 1.0**: at 15.25 GB up against
59.84 GB down (0.255), reaching 0.4 needs `0.4 x 59.84 = 23.94` GB uploaded, so **+8.7 GB**. Reaching
1.0 would need +44.6 GB, and nobody asked for that. Freeleech downloads never move the denominator,
so grabbing more freeleech cannot make this worse.

Both conditions are enforced here rather than remembered: the freeleech required-flag on the
indexer covers the automation, and the single slot takes care of itself as long as nothing else is
grabbed by hand. **A manual download from the site is the one path with no safety net: check the
FREELEECH tag before clicking.**

### The autobrr trap that gets accounts banned

Read before configuring autobrr, because this is a ban, not a warning. From a site notice dated
2026-08-07:

> We've had to ban over 500 users in the past period due to a misconfiguration in autobrr's IRC
> settings that was hammering our IRC network with rapid connect/disconnect cycles, effectively a
> DDoS against our own infrastructure.

The setting is **Settings → IRC → the announce connection → Identification → Mechanism**. Setting it
to SASL (plain) or NickServ makes the bot reconnect every one or two seconds. **Leave it on None**,
which is the default.

And the three things their notice states outright, which contradict the generic autobrr advice found
everywhere else:

- the bot nick does **not** need registering on IRC;
- SASL and NickServ authentication are **not** needed;
- the bot **autojoins** the right channel with no extra configuration.

The bot nick is `<your-username>_bot` and the channel is `#tlannounces`. Verify with
`/whois <your-username>_bot` from your own client: it should be in that one channel and nowhere else.

### Links given by staff

- Common mistakes: <https://wiki.torrentleech.org/doku.php/common_mistakes>
- Rules wiki: <https://wiki.torrentleech.org/doku.php/>
- Guide for newly invited users, i.e. how not to get disabled again:
  <https://wiki.torrentleech.org/doku.php/newly_invited_users/>
- autobrr on their wiki: <https://wiki.torrentleech.org/doku.php/autobrr>
- Forums: <https://forums.torrentleech.org/>
- How to maintain your ratio: <https://forums.torrentleech.org/t/how-to-maintain-your-ratio/78082/>
- **Newest freeleech torrents, last 10 minutes** (the one worth bookmarking):
  <https://www.torrentleech.org/torrents/browse/index/facets/tags%3AFREELEECH_added%3A%255BNOW%252FMINUTE-10MINUTES%2520TO%2520NOW%252FMINUTE%252B1MINUTE%255D>
- qBittorrent + autobrr setup: <https://seedit4.me/kb/articles/qbittorrent-autobrr-setup/152>
- autobrr indexer configuration: <https://autobrr.com/configuration/indexers>
- Optimising upload speed and building ratio:
  <https://seedit4.me/kb/articles/optimizing-seedbox-upload-speeds-and-building-your-ratio/146>

## How this world actually works

The mechanics are the same on almost every private tracker, and they are not obvious from the
outside. Worth understanding once rather than learning by getting disabled.

### Ratio, and the buffer that really governs your life

Ratio is `uploaded / downloaded` **as counted by the tracker**, and the number to watch is not the
ratio itself but the **buffer**: how many GB you can still download before dropping under the
threshold.

```
buffer at a threshold T = uploaded - (T x downloaded)
buffer at 1.0           = uploaded - downloaded
```

A buffer of 40 GB means you can grab a 40 GB non-freeleech release and land exactly on the
threshold. That is the number to look at before clicking anything, not the ratio.

Two things bend the arithmetic in your favour, and both are the tracker's gift rather than yours:

- **Freeleech** torrents do not add to `downloaded`. The denominator never moves, so freeleech can
  only ever help. This is why "freeleech only" is the standard remedy for a broken ratio.
- **Upload multipliers** (double upload, x2 events) credit more than you actually sent.

### Where upload actually comes from

You only upload when somebody else is downloading the same torrent **and picks you**. That gives
exactly two viable strategies:

1. **Be early.** A release minutes old has hundreds of leechers and a handful of seeders, so
   everyone asks you. This is the reliable way, and it is why tools exist purely to grab new
   releases the second they are announced.
2. **Be the only one.** A large, niche torrent nobody else seeds means every single person who ever
   wants it takes the whole thing from you. Fewer customers, but each pays a lot. Measured here on
   2026-08-20: a 73.8 GB freeleech disc set with 1 seeder and 33 leechers is worth more than a
   popular 5 GB release with 50 seeders competing for the same demand.

What does **not** work is seeding an old catalogue. Measured the same day: 16 torrents with 20 to 259
seeders each and 18 leechers between all of them produced 0 KB/s. Being reachable is necessary and
not sufficient; somebody has to actually want the file.

### Hit and run, which is a separate account killer

Ratio and hit & run are two independent ways to lose an account. H&R is per torrent: download it and
fail to seed it back for the required time or ratio and you collect a warning, whatever your overall
ratio looks like. **Three uncleared warnings disable the account on TorrentLeech.** They clear by
seeding the offending torrents, which is why deleting data to free space is the most expensive
cleanup available.

The practical rule: **never delete a torrent that still owes seeding time**, and that is exactly what
`seed-cleanup.py` enforces here (240 h or ratio 1.0 for private trackers before anything is removed).

### Bonus points, the passive income

Most trackers pay bonus points per GB held per hour of seeding, redeemable for upload credit,
freeleech tokens or class upgrades. This is the one lever that works even when nobody downloads from
you: keep data seeded and the points accumulate. Worth checking each site's shop before grinding for
ratio the hard way. Balances on 2026-08-20: DigitalCore 543.5p, TorrentLeech 130.37 TL points.

### Slots and classes

Download slots are limited by user class, and classes are earned with upload and time.
TorrentLeech starts at **one slot**, and a partially downloaded torrent occupies it, so a single
stalled grab blocks everything. Build ratio and the limit rises.

### The stable regime, once the ratio is at 1.0

The rules change once there is a buffer, and this is the policy to keep:

| Buffer | What you can do |
| :--- | :--- |
| comfortable, say 50 GB+ | grab non-freeleech freely, but always seed each torrent past its H&R requirement |
| thin, under ~20 GB | back to freeleech only until it recovers |
| negative | you are below the threshold and on a clock, freeleech only, no exceptions |

Two habits keep it there: **seed everything you take for longer than required** (that is what builds
the buffer while you sleep), and **prefer new releases over catalogue** when you have a choice, since
they are the only ones anybody is waiting for.

And the automation-specific one, which is what broke this account in the first place: an *arr stack
grabs whatever matches its profile, so **the buffer rule has to live in the indexer configuration,
not in your head**. The freeleech required-flag is that rule expressed as a filter.

## DigitalCore

- Freeleech periods and **upload multipliers** are generous enough to make the on-site ratio look
  nothing like the client's. Check the profile page.
- **Bonus points** accumulate and can be spent; worth reading their shop before grinding ratio.
- The per-torrent **`Connectable`** column under *Activity & Logs → Transfers* is the best inbound
  check available anywhere: it is a third party telling you whether the world can reach your port.
  It read `No` on all seven torrents while behind CGNAT and flipped to `Yes` within minutes of the
  public IP arriving.

## Controls applied on this box

- **TorrentLeech is freeleech-only at the source.** `requiredFlags = [1]` (`G_Freeleech`) on the
  TorrentLeech indexer in **both** Radarr and Sonarr, so a non-freeleech grab cannot happen by
  accident while the ratio recovers. Radarr rejects the save with a 400 while the indexer is
  unreachable; `?forceSave=true` on the API call gets around it.
- **`seed-cleanup.py` never touches a private torrent that still owes its tracker**: public torrents
  are dropped as soon as the library has the file, private ones seed until 240 h or ratio 1.0. See
  the deletion policy in the README.
- Automation is the reason this happened at all: the *arr stack grabbed whatever matched, nobody
  read the site notices, and warnings sat unseen for six days. **If a tracker's rules need a human
  to remember something, encode it as a filter instead.**

## Two things worth building, and how

Both were asked for on 2026-08-20 and neither is built yet. Written down so the design does not have
to be reinvented.

### Watching the trackers from Grafana, with Telegram alerts

The stack for this already exists here: `media-metrics.py` pushes to Pushgateway, Prometheus scrapes
it, Grafana alerts to Telegram. The only new part is getting the numbers out of each site.

- **Where the numbers live.** Ratio, uploaded, downloaded, hit & run count, bonus points and class
  are on each site's own profile page. There is no public API on TorrentLeech, so it means fetching
  the page with a stored session cookie and parsing it. DigitalCore has an API (Prowlarr already
  uses it) and may expose stats without scraping, worth checking first.
- **Metrics to export**, one series per site: `tracker_ratio`, `tracker_uploaded_bytes`,
  `tracker_downloaded_bytes`, `tracker_buffer_bytes` (the derived one that matters),
  `tracker_hit_and_run`, `tracker_bonus_points`.
- **The three alerts worth having**, all of which are the ones that would have caught this incident
  weeks earlier:
  1. **buffer below a floor** (say 20 GB), which is the early warning;
  2. **hit & run count increased** since the last scrape, which is the "you are collecting warnings
     and not reading the site" alarm;
  3. **ratio under the site minimum**, which is the last-chance one.
- **The fragile part, stated honestly**: cookie-based scraping breaks whenever a site redesigns, and
  a session cookie expires. Expect to re-paste a cookie into `.env` every few months, and make the
  script log loudly rather than silently exporting zeros, since a zero looks like a catastrophic
  ratio and would page you at 3am.

### Not having to watch the freeleech feed by hand

The link staff gave is a search filtered to freeleech added in the last ten minutes. Refreshing it
manually works but misses the whole point, which is being early. Two ways out:

- **autobrr** (their own wiki recommends it, and links a qBittorrent guide). It joins the tracker's
  IRC announce channel and reacts the instant a release is announced, which is seconds rather than
  the minutes an RSS poll costs. Filters on freeleech, size and category, and hands the torrent
  straight to qBittorrent. With one download slot, the filter must cap concurrency at one.
- **qBittorrent's own RSS** pointed at that freeleech URL, with a matching rule. Zero new
  components, but polls on a timer, so it arrives after the autobrr crowd.

Either way the shape is the same as everything else here: **the rule lives in the tool, not in a
human remembering to check a bookmark.**

## Verifying inbound, the only measurement that matters

```bash
# the port, from outside the network entirely
nc -vz <public-ip> 57429

# incoming peers: an inbound peer carries the I flag
docker exec qbittorrent curl -s \
  "http://localhost:8080/api/v2/sync/torrentPeers?hash=<hash>&rid=0" | grep -o '"flags":"[^"]*"'

# what the router thinks its own WAN address is, no login needed
# (inside 100.64.0.0/10 means CGNAT, see seeding-and-ratio.md)
```

And a fresh shareable speedtest, which trackers ask for, comes out of the speedtest container:

```bash
docker exec speedtest-tracker speedtest --accept-license --accept-gdpr -f json
# result.url is a public speedtest.net link
```
