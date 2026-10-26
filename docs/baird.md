# Baird, which channels there are

Two services, one job each. **Baird decides which channels there are** and writes them as one M3U.
**bifrost plays them** and puts them in Jellyfin. bifrost reads Baird's list like any other list and
knows nothing else about it; Baird writes a standard M3U and knows nothing about who reads it.

```
config/acestream/upstream.json ──► shared lists ─┐
web pages with acestream:// links ───────────────┼──► baird ──► baird.m3u ──► bifrost ──► Jellyfin
the engine's catalogue (aceserve /search) ───────┘   «which»                  «how»
```

| Source | What Baird does with it |
| :--- | :--- |
| The shared lists | Passes their channels through as they wrote them (logo, guide id, quality markers), minus what was switched off. A hidden group also hides the channels that join it later, and a single channel can be switched back on inside it |
| A web page that links `acestream://<id>` buttons | Reads every id on it and lists them under one channel name you give it, so a page with twelve buttons becomes one channel with twelve sources |
| The engine's catalogue | The same list [acestreamui.app](https://acestreamui.app) shows, about 1,700 channels. You pick categories, narrowed to some countries if you like ("sport from Spain and the UK"), and switch single channels in or out. Infohashes become the content ids bifrost plays by (`get_content_id`, cached for good in `ids.json`) |

The page is at `https://baird.<domain>`, tailnet only, with a second skin at `/retro`. Named for
John Logie Baird, who showed the first television picture of a face in 1925.

## The shared lists, and what they drop

The lists rotate content ids several times an hour, so Baird reads them every 2 minutes
(`LISTS_EVERY`); the catalogue and the pages every 30 (`REFRESH`).

Their publisher also prunes channels and sources that still play. Baird keeps some of what they
drop, for three days at most (`KEEP_DROPPED`), and asks the player how each source did: `PLAYER_URL`
points at bifrost's `/api/channels`, read as a hint; when it does not answer, the time limit is all
there is.

- **Every source the lists carry now goes in.** Kept ones come on top, at most three per channel
  (`MAX_KEPT`): those the player saw play first, then the most recently dropped. bifrost tries three
  or four sources per open at most (25 s each, 90 s per channel), so more is dead weight. On
  2026-10-26, 366 of bifrost's 508 sources were kept ones, nearly all never tried.
- **A source the player saw fail goes at once.**
- **A channel the lists dropped whole stays only while one of its sources has played.** Otherwise it
  goes with the list, instead of lingering with twenty untried sources. This
used to be bifrost's job; it is Baird's now because it is about the list, not about playing it, and
because only the one that decides can tell a channel pruned upstream from one you switched off. So
bifrost runs with `KEEP_DROPPED=0` and plays the list exactly as Baird writes it.

## Keeping junk out

Nothing reaches the list without a decision:

- **A group the shared lists start carrying comes in hidden**, marked "new upstream" on the page
  until it is switched on. The first read takes every group as it was, so deploying Baird changes
  nothing in Jellyfin.
- **A category only brings in live channels**: at least one source the engine sees as available.
  A dead one comes in by itself once it is live again. Channels switched on by hand come in anyway.
- **A click that would add more than 50 channels asks first**: a big category, or dropping the
  country filter.

## When something breaks

Every failure falls back to "as it was", never to an empty Jellyfin:

| What breaks | What happens |
| :--- | :--- |
| A shared list | Its other routes (kubo, ipfs.io, dweb.link), then its last good copy in `appdata/baird/lists/` |
| A gateway answering 200 with junk | A read with no channels counts as down, and the last copy stands in |
| Baird | bifrost plays the last `baird.m3u` it read, kept in its state across restarts. Nothing changes until Baird is back. The "Acestream source is failing" alert names it |
| The engine | Nothing plays anyway; the catalogue's last copy (`catalogue.json`) stays |
| Baird's state | `appdata/baird` is in the nightly backup. `choices.json` is what matters: every decision on the page |
| The shared lists, for good | The catalogue and the pages do not depend on them |
| Baird itself, and it has to go now | The rollback in `config/bifrost/lists.json`: uncomment the two shared lists and comment out Baird's line. bifrost rereads the file on its next pass, with no restart. For more than a day, also set its `KEEP_DROPPED` back to `72h` |

The second node does not run Baird: its bifrost reads `config/acestream/upstream.json` directly,
with `KEEP_DROPPED=72h`, as before Baird existed.

## Files

`appdata/baird/`: `choices.json` (the decisions), `baird.m3u` (the list, also at
`http://baird:8089/baird.m3u`), `upstream.json` (the shared lists with what they dropped lately),
`catalogue.json`, `ids.json`, `lists/` (last copy of each shared list), `logos/` (catalogue logos,
served from here because they are http URLs an https page refuses).

## What it does not do

- It reads only `acestream://` links in a page's HTML. A page that builds its links in JavaScript
  gives nothing, and adding it fails with "no acestream links". One page is one channel.
- It does not check that a source plays; bifrost does, and orders dead ones last.
- No login of its own: like every tailnet-only page here, being on the tailnet is the access check.
