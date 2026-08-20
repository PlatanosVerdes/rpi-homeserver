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
