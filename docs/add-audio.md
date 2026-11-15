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
  reasons in [audio-convert.md](audio-convert.md). An AC3 dub at the same speed is copied
  untouched; DTS, TrueHD, EAC3 or a PAL dub is converted to AC3 640k.
- Tagged `es-ES`, named `Castellano`, **not default**: the original audio stays what plays first,
  `--default` changes that.
- The file is checked before it replaces anything: same duration, one more stream, the new track
  in AC3, and the new track measured against the dub it came from (the mapping must hold inside
  the new file). The old file is kept as `.original`, as [audio-convert.md](audio-convert.md) does.

Which dub track it picks: a `spa` track whose title does not say Latino, Mexican, neutral or
commentary, preferring one titled Castellano, then AC3, then more channels. A track mislabelled by
its release (Latin-American tagged `spa` with no title) cannot be told apart; `--track N` names one.

## Resources

It re-runs itself in a systemd user scope capped at 1 GB, page cache included, with no swap, at
`nice 19` and `ionice -c3`. Measuring decodes three hours of audio twice and writing the result is a
10 GB copy: run bare on 2026-11-15, the page cache it filled pushed every other service into swap
and the uptime probe called the Pi down for three minutes. It shares the conversion lock with
`audio-compat.py` and the audio page, so it never runs alongside them.

## Tests

```bash
python3 -m unittest scripts/sync/test_add_audio.py
```

The sync maths on synthetic loudness curves, track choice, and three whole runs on generated
eight-minute films: a PAL DTS source with staggered starts (converted and in sync), a same-speed
AC3 source (copied and in sync), and a source with a cut (refused, library file untouched). The
whole runs need ffmpeg and mkvmerge; `ADD_AUDIO_TEST_DIR` puts their files on the data disk.
