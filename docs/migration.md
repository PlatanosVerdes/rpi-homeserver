# Migration: from a bespoke script to the tools

**This file is temporary and dies with the work it tracks.** It exists for the window where two
implementations of the same job are in the repo, one running and one parked, and the only honest
state is "watching". When the parked one is deleted, so is this page and its line in the README.

For how retention works now that it works, read [lifecycle.md](lifecycle.md). For why this was
decided at all, read the qbit_manage section of [../PENDING.md](../PENDING.md). This page is only
about the handover.

## What moved

| The job | Owned by | It replaced |
| :--- | :--- | :--- |
| Deleting a torrent and its data once its tracker is paid | qbit-manage `share_limits`, one group per tracker | `scripts/trackers/seed-cleanup.py` |
| Removing what a tracker has disowned | qbit-manage `rem_unregistered` | nothing, this was never done |
| Clearing a stalled download that will never finish | qbit-manage `share_limits`, the `stalled` group | nothing, it sat at 0% forever |
| Knowing the library has let go of a file | qbit-manage `tag_nohardlinks` writing `noHL` | the script's own `stat()` on every file |
| Deleting the film after it is watched | Maintainerr, unchanged | unchanged |

## Done

- **2026-08-21** qbit-manage adopted, writing tags only, everything that deletes off.
- **2026-08-24** the cutover, in one step rather than the planned three weeks. Two numbers made it
  safe: deletions go to a recycle bin instead of being unlinked, and the first pass was computed
  against the live client beforehand and would have deleted nothing.
  - `share_limits: true` and `cleanup: true` on every group; `rem_unregistered: true`.
  - `seed-cleanup.py` commented out of `scripts/crontab`, left in place.
  - Its two alert rules, `homelab-seed-cleanup-failed` and `homelab-seed-cleanup-stale`, paused with
    `isPaused: true`, because the stale one fires three hours after the script stops.
- **2026-08-24** one group per tracker, with numbers measured rather than guessed. Each sits above
  what its site asks, because qBittorrent counts seeding time while announces are rejected and the
  site does not: TorrentLeech 360 h against 240, from a gap measured at 26 to 118 h; DigitalCore
  180 h against 120, from a gap measured at 9 to 23 h on their own `My Seeds` page; retrotoon 120 h
  against 72; C411 150 h against 96; `private` 240 h as the floor for a site with no group yet;
  `public` 2 h.
- **2026-08-24** `include_any_tags: [noHL, ratio]`, so a group also matches what autobrr grabbed.
  Those torrents carry no category, so `tag_nohardlinks` never inspects them and they can never be
  tagged `noHL`: 17 torrents and 538 GB that no group would otherwise have touched.
- **2026-08-25** the first real deletion, 76 GiB, and the three things it taught, all fixed:
  - the recycle bin lives under `downloads/`, so the orphan scan counted it as data nothing owns and
    raised an alert an hour later. `.RecycleBin` and `orphaned_data` are skipped now.
  - the bin holds the disk for as long as it keeps files, so freeing 76 GiB moved free space *down*.
    `empty_after_x_days` went from 7 to 2.
  - a stalled download matches its tracker's group, which waits on seeding time it can never have,
    so it would have stayed at 0% forever. Hence the `stalled` group, at priority 1.
- **2026-08-25** autobrr's filters and Maintainerr's rule exported into `config/`, with
  `scripts/ops/config-export.py --check` on every deploy, so the two tools that only existed in the
  nightly archive are readable in git.

## What is left, and it is only watching

Nothing to build. A week of real deletions, from the first one on **2026-08-25**, so this closes
around **2026-08-31**. Three things to check, in order of how much they would cost:

```bash
# 1. Was anything deleted that still owed its tracker? The site is the only authority.
sudo ls /mnt/data/downloads/.RecycleBin        # what went, and when
# then open the tracker: DigitalCore's My Seeds shows Seed Time Left, and it must read Done
# for anything that was removed.

# 2. Is any torrent owing hours with its clock stopped? Must stay at zero.
curl -s 'http://localhost:9090/api/v1/query?query=sum(tracker_hnr_at_risk)'

# 3. What did the tool actually do, in its own words.
docker logs --since 24h qbit-manage | grep -iE "deleting|recycle|Total"
```

When that week is clean, delete in one commit: `scripts/trackers/seed-cleanup.py`, its two paused
alert rules, the 18 panels on the Retention and Disk Usage dashboards that read the metrics it used
to push, the paragraphs in `README.md`, `private-trackers.md`, `architecture.md` and
`seeding-and-ratio.md` that still describe it as the thing that deletes, and this file.

## What no tool does, and is not being scripted

- **Telling an arr that a release was bad.** qbit-manage removes a stalled torrent but cannot
  blocklist it, and Radarr's `autoRedownloadFailed` only fires on a download it was told failed, not
  on one that quietly vanished. So an arr can grab the same dead release again. Cleanuparr is the
  tool for this and its whole configuration lives in a SQLite database under `/config`, which is why
  it is not here: it would move the retention policy out of git. The alert firing twice on one title
  is the signal that it is time to reconsider.
- **The three torrents in `cross-seed-link`.** No group matches them, deliberately, since a
  cross-seed shares its bytes with the torrent that downloaded them. It also means they outlive the
  film. Nothing is at risk; the disk is simply never returned.
- **Pushing autobrr and Maintainerr config back from git.** The export is one-way on purpose: a
  wrong quality profile downloads the wrong file, a wrong Maintainerr rule deletes the library.

## How to go back

One line, and nothing else moved:

```bash
# 1. uncomment the script in scripts/crontab, and
# 2. set share_limits: false under commands in config/qbit-manage/config.yml, and
# 3. drop isPaused from the two homelab-seed-cleanup-* rules in config/grafana/alerting/rules.yml
```

Order matters: the script and the groups must never delete at the same time, which is the one
arrangement worse than either alone. Anything already in `.RecycleBin` stays there for two days and
can be moved back by hand.
