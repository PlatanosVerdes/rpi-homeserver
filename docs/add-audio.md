# Add audio: a dub from another release

`scripts/sync/add-audio.py` takes the dubbed track (Castilian Spanish by default) out of another
release of the same film and adds it to the library file, synced. The library file keeps its video
and its own audio; the dub is one more track to pick from the player's audio menu.

```bash
scripts/sync/add-audio.py "<library file>.mkv" "<other release>.mkv" --dry-run   # measure only
scripts/sync/add-audio.py "<library file>.mkv" "<other release>.mkv"             # add it
```

A torrent cannot fetch one track out of an `.mkv`, so the other release is downloaded whole. Its
picture quality does not matter: a 2 GB 720p or a DVD rip carries the same AC3 5.1 dub as a
remux. Once the track is in, that download is only owed to its tracker: a public one can go at
once, a private one after its seed term ([private-trackers.md](private-trackers.md)).

## How the sync is found

Both releases share music and effects; only the voices differ. The script reduces each track to its
loudness, 200 values a second, and finds the mapping `t_source = a * t_target + b`:

- **`a`, the speed.** 1.0 for two releases of the same master. 0.959 for a PAL DVD, which runs
  23.976 fps film at 25 fps, 4% faster and 4% higher in pitch. Undoing it resamples, which also
  puts the pitch back.
- **`b`, the offset**, from a few ms to minutes (a studio logo one release has and the other
  does not).

When the other release also carries the library file's language, that track is the one aligned:
the same English against the same English matches far more sharply than English against a dub.

The line is fitted through 16 two-minute windows spread over the film. **Only one clear window may
disagree**; more, and the source is refused. A source cut differently (an extended edition, a
missing scene) puts every window after the cut on another line, and discarding those as noise
would add a track that drifts out of sync from the cut on. What it does **not** catch: a cut in the
last few minutes, past the last window, which is the credits.

Tracks inside a file start tens of ms apart (`start_time`), and ffmpeg decodes from a track's first
sample, so each loudness curve is put back on the file's own timeline before anything is compared.
Leaving that out once measured 80 ms wrong.

## What it writes

- **Always AC3**, up to 5.1: the one codec the TVs here play without Plex re-encoding it, for the
  reasons in [audio-manager.md](audio-manager.md). An AC3 dub at the same speed is copied
  untouched; DTS, TrueHD, EAC3 or a PAL dub is converted to AC3 640k.
- Tagged `es-ES`, named `Castellano`, **not default**: the original audio stays what plays first,
  `--default` changes that.
- The file is checked before it replaces anything: same duration, one more stream, the new track
  in AC3, and the new track measured against the dub it came from (the mapping must hold inside
  the new file). The old file is kept as `.original`, as [audio-manager.md](audio-manager.md) does; the page lists them and deletes one when asked.

The languages it can add are `scripts/sync/languages.json`: 26 of them, Castellano
by default, each with the codes files tag it with, the words Radarr and release titles use for it, and
for three the variant that is not it (Latin American Spanish, Québécois French, Brazilian Portuguese).
Adding a language is one entry there; the page reads the same file.

Which dub track it picks: a `spa` track whose title does not say Latino, Mexican, neutral or
commentary, preferring one titled Castellano, then AC3, then more channels. A track mislabelled by
its release (Latin-American tagged `spa` with no title) cannot be told apart; `--track N` names one.

## Resources

It re-runs itself in a systemd user scope capped at 1 GB, page cache included, with no swap, at
`nice 19` and `ionice -c3`. Measuring decodes three hours of audio twice and writing the result is a
10 GB copy: run bare on 2026-11-15, the page cache it filled pushed every other service into swap
and the uptime probe called the Pi down for three minutes. It shares the conversion lock with
`audio-compat.py` and the audio page, so it never runs alongside them.

## From the page: audio-manager

The **Add audio** view of the audio manager page (`https://audio.<domain>`,
[audio-manager.md](audio-manager.md)) lists the films that lack a language, and for each
one whether a release in that language is out there. The films come from Radarr, but what each file
holds is read from the file itself with ffprobe, again only when its size or time changes: Radarr says
"Spanish" for a Latino track too, so a film with only Latino would never be offered Castellano. Nothing is downloaded unless Add is pressed. The `services/audio-manager` service does
all of it in Go, one film at a time; the only Python it runs is `add-audio.py` itself, as it runs
`audio-compat.py` for Re-encode.

**Availability** comes from a search the service runs in the background, one film every
`SWEEP_EVERY` (1 min), never-searched films first, each film again after `SWEEP_REFRESH` (14 days).
It only searches for `SWEEP_LANGUAGE` (Castellano): a film that has it, has a request for it under way,
or already has a release naming it or from a site in it is left alone, since Add searches afresh. A
film whose request failed is searched again, as a new release may carry it, and so is one whose file
lacks it again after a request added it: Radarr replaced the file with an upgrade. Other
languages are ranked from the same searches; for a film the sweep left alone, use Search again. A
search returns the same releases whatever the language, so each film is searched once and every
language is ranked from that result: the row says how many releases name the language in their title,
how many come from a site in that language, and how many could only be known by probing. The
Available filter keeps the films where the first two are not zero. **Search again** on a row searches
that film now, with each indexer shown as it answers. A title that names the language is likely; the
rest can only be known by probing, which Add does. About 60 films take an hour the first time; after that only
the films with nothing found are searched, about 20 every two weeks. Each search is two queries per
indexer, so at one a minute a tracker sees at most one query every 30 s, less than Radarr's own RSS.

1. **Search.** Prowlarr is asked on every enabled indexer, by IMDb id and by title (NOBS answers
   nothing by IMDb id, and a title match only counts with the same IMDb id). Releases go in order of
   how likely they are to carry the language: a title that names it, then a site in that language,
   then MULTi/DUAL (not on a site in a third language: on C411 that means French), then public before
   private, freeleech before counted, smaller first. A site in a third language goes last: on Split,
   C411's MULTi VFF releases were probed first and carried French and English only. At most 15 GB, never titled Latino.
   A release an earlier request already downloaded goes first (see step 3).
2. **Probe before downloading.** A Matroska file lists its tracks in its first few hundred KB. The
   release is added stopped, with only its largest `.mkv` wanted, sequential and capped at 2 MB/s,
   unless that `.mkv` is under half the torrent: a release packed in RAR files shows only its sample as
   `.mkv` (Michael's XPERT_HD WEB on DigitalCore), and is deleted before anything downloads. Otherwise it is
   then watched every 3 s: once 2 MB from the file's start are in, ffprobe reads the track list and
   the torrent is stopped. No track in the language (add-audio's own pick: not Latino, not commentary)
   and it is deleted after a few MB; measured on a YTS release, 12.8 MB. A release that shows no
   track list within 64 MB is given up the same way. The header also gives its length: one more than
   5 min away from the film's, at any frame-rate change add-audio can undo, is another cut (an
   extended edition) and is dropped too.
   For a language with variants it rejects (Castellano against Latino), a track tagged `spa` is
   confirmed only when its title, the release title or the site says Castellano; a MULTi release may
   tag Latino as `spa`. An unconfirmed one is dropped and kept for last: it is probed again only once
   nothing confirmed has the dub, and the result then says to check it.
   Each probe is listed under its request: the release, how many MB it took and which tracks it had.
3. **Download and add.** A release with the track is downloaded whole and handed to add-audio, which
   still measures the sync and refuses another cut or a source under 90% of the film's length. It runs
   only while the raspi's 5-minute load is under `ADD_AUDIO_MAX_LOAD` (4); a finished download waits
   for it, as decoding and muxing a film beside a busy raspi has made it stop answering. A dub
   that runs longer than the film (longer credits) is cut at the film's end. A refusal or a download
   stalled for 48 h moves to the next release, up to two whole downloads and eight probes per film.
   A release still seeding from an earlier request is not downloaded again: when Add finds it complete
   in qBittorrent under `audio-source`, it reads its tracks and hands it straight to add-audio, so a
   request that failed for a reason fixed since costs nothing to repeat.

What keeps a probe from costing an account anything:

- **A private site is probed only where deleting a few MB owes nothing**: its hit and run in
  `config/trackers/rules.json` starts at `starts_at_progress` (completion when absent), and the probe
  stays a quarter under it. NOBS starts at 50%, DigitalCore at 10%. A private indexer with no entry
  there is never used.
- **The probe stops itself** at that limit or at 64 MB, whichever is lower, whatever the swarm's speed.
- **A whole download is never deleted here.** `audio-source` is in qbit-manage's `nohardlinks` list, so
  it is tagged noHL and its tracker's share-limit group seeds and deletes it. audio-manager adds a `keep`
  tag, which every group skips, and removes it once add-audio is done: without it a public download
  would be deleted 2 h after finishing, possibly while add-audio still reads it.
- **A budget of counted download**, `DUB_BUDGET_GB` (100 by default; public and freeleech count
  zero), across all requests. A download is charged its listed size when it starts and settled to what
  qBittorrent actually fetched when it ends; NOBS's site-wide freeleech is not in Prowlarr's flags, so it
  still counts.

The page shows each request's state and why it ended; the alert Telegram chat gets each download
and result. Its state is in `appdata/audio-manager/add-audio/`: `state.json` for the jobs and
`searches.json` for the searches, without download links (Add searches again when it starts).

`add-audio.py` runs inside the container here, so the container's own 1 GB memory limit is what caps
it, page cache included; it has no systemd scope there and says so in its output.

## Tests

```bash
python3 -m unittest scripts/sync/test_add_audio.py
cd services/audio-manager && go test ./...
```

The sync maths on synthetic loudness curves, track choice, and three whole runs on generated
eight-minute films: a PAL DTS source with staggered starts (converted and in sync), a same-speed
AC3 source (copied and in sync), and a source with a cut (refused, library file untouched). The
whole runs need ffmpeg and mkvmerge; `ADD_AUDIO_TEST_DIR` puts their files on the data disk.
