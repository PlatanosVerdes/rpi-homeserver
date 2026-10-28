# Audio convert: fixing a re-encoded file from a page

When Plex or Jellyfin has to re-encode a film's audio for a TV, the alert "Audio is being
re-encoded" fires and the file lands on `https://audio.<domain>` (or `http://audio` on
the LAN) with what it would take to stop it happening: which track an AC3 5.1 would be built from,
how much bigger the file gets, and how long it takes. **Convert** runs the conversion there and
then, with a progress bar and the time left; otherwise the nightly pass at 01:30 gets to it.

The page is reachable from the tailnet only: Caddy serves it on the Tailscale address, it is not
behind Cloudflare Access, and nothing publishes it.

## Who does what

Three pieces, and each one does a single job:

| Piece | Job |
| :--- | :--- |
| `services/pi-metrics` (`recodes.go`, `plan.go`) | Watches the sessions, decides whether a file change can help, and writes the plan into `appdata/pi-metrics/audio-recodes.json` |
| `scripts/sync/audio-compat.py` | The only code that converts: remux, verify, rename. Nightly over the library, `--file` for one, `--scan` to list what the nightly pass would convert |
| `services/audio-convert` | The page: lists the plans and the library scan, queues a file, runs `audio-compat.py --file`, shows its progress |

The page never touches ffmpeg itself. The container carries Python and ffmpeg and runs the very
same script the cron runs, mounted from `scripts/sync/` rather than copied into the image, so a
change to how a file is converted reaches both on the next pull, with no rebuild.

## What the plan says, and where it comes from

pi-metrics already reads every session once a minute. For a session whose audio is re-encoded, on a
client that is not a browser, with the video copied, it asks the server about the file: its title,
its duration and its audio tracks (Plex's `/library/metadata`, Jellyfin's `NowPlayingItem`). From
that it works out the same thing `audio-compat.py` would:

- **The source tracks** are the main track of every language. The lead one is the track the client
  was playing when the session says which (Plex's selected stream, Jellyfin's `AudioStreamIndex`),
  else the one flagged default, else the first: `default_audio_index` in the script. The default
  flag is what the release chose as its main mix, which on a foreign film is the original language
  rather than a dub. Every other language adds its first track that is not a commentary. The page
  passes them to the script as repeated `--track`; the nightly pass works them out the same way.
- **Whether a language needs work** is decided by its main track: it must be in a refused codec,
  and nothing else in the file may play in its place. A MULTi release with French and English both
  in TrueHD gets an AC3 for each; with only the French one, picking English on the TV played French. A stand-in is a track in a codec
  nobody refused, in the same language, with as many channels (up to 5.1), and not flagged as a
  commentary. A DTS-X mix with an MP3 beside it, or a TrueHD one with a stereo AC3 commentary, still
  gets the AC3; a DTS 5.1 with an AC3 5.1 of the same mix does not. `assess` in the script and
  `decide` in `plan.go` are the same rule.
- **The target** is AC3 at 640 kb/s, with the source's channels up to 5.1. A 7.1 source becomes 5.1.
  On the page the codec can be changed per file, among the ones `TARGETS` in the script defines
  (AC3 5.1, EAC3 5.1, AAC stereo); AC3 stays selected by default and is the only one the nightly
  pass adds. A codec the client refused is offered but disabled, since it would be re-encoded the same.

## Before any client plays a file

The page's "In the library" section, folded by default, lists every library file the nightly pass
would convert, from `audio-compat.py --scan`: the same rule, run over the library without
converting anything. The page runs it on start, every `SCAN_INTERVAL` (6 h by default) and after
each conversion, keeps the result in `appdata/audio-convert/library.json`, and leaves out files the
ledger already lists. Those files can be converted from there too, ahead of the night run.
- **The cost** is 640 kb/s times the duration for each added track, about 576 MB for two hours. On top of that, the old
  file stays beside the new one as `.original` until someone deletes it.
- **The time** is the duration divided by how fast conversions run. That starts at 20× realtime,
  measured on this Pi, and becomes the mean of the last five real conversions as they happen,
  the nightly ones included.

The script reads the file with ffprobe when it runs and decides again. If the server's description
and the file disagree, the file wins; the page shows the track the script actually used once it starts.

## What is listed but cannot be fixed

Some re-encodes are listed greyed out, with the reason, because pressing a button would change
the file and nothing else:

| Shown as | Why a new track does not help |
| :--- | :--- |
| The client turned direct play off | Plex: the client did not ask for direct play (`cause=no_direct_play`), so the server built a stream whose profile takes only AAC. An added AC3 is re-encoded just the same. Enable **Force Direct Play** in the client |
| The client refuses AC3 too | AC3 is what would be added |
| Its main track is already playable | The track the client plays is in a codec no client has been refused |
| It already has a stand-in | Same language, enough channels, a playable codec: pick that track in the player |
| Not in films/ or series/ | Live TV and anything outside the library has no file to change |

`no_direct_play` entries teach the nightly pass nothing and are never put at the head of its
queue; only `cause=codec` sessions are. Why the two are told apart is
[below](#which-codecs-get-converted-is-learned).

## Which codecs get converted is learned

`audio-compat.py` used to carry a list of codecs "the TV decodes without help". A list kept by hand
is wrong the day a client disagrees with it, so there is no list. But "a client had this codec
re-encoded" is not proof the client cannot play it either, and the difference is the `cause` label
on `media_transcode_sessions`:

| `cause` | Meaning | Can a file change fix it |
| :--- | :--- | :--- |
| `codec` | Direct play was allowed and the client's profile refused this audio codec | Yes, an added track in another codec |
| `no_direct_play` | Plex: the client did not ask for direct play | No |
| `other` | Jellyfin: its `TranscodeReasons` name something other than the audio codec | No |
| `unknown` | Nothing says which | Not known, so nothing is done |

`no_direct_play` is the trap. The Plex app on a Samsung TV turns direct play off itself for some 4K
files, and Plex then builds a DASH stream (video and audio cut into short segments over HTTP) whose
profile takes only AAC. EAC3 is re-encoded; once an AC3 track is added, the AC3 is re-encoded just
the same. Plex's session API does not say whether direct play was requested, only the decision
request in its log does (`directPlay=0` or `1`, keyed by the transcode session's id), so pi-metrics
mounts Plex's `Logs` directory read-only. Those requests are logged at debug level, Plex's default;
with debug logging off, every Plex session reads `unknown`. Jellyfin reports its own reasons, and
`AudioCodecNotSupported` is the one that means `codec`.

The `playback` collector writes `appdata/pi-metrics/audio-recodes.json` from `codec` sessions only:
the audio codec, which clients, and the last hundred files it happened on. It also requires the video
to have been copied. When the video is re-encoded too, the audio goes along whatever its codec, and
Jellyfin's Live TV does that to AAC every day.

The same file lists sessions whose client turned direct play off, marked as not fixable and
teaching no codec, and each entry carries a conversion plan worked out from the server's description
of the file: which track the AC3 is built from, the extra size, the duration. That is what the page
shows, and the alert links there.

At 01:30 `audio-compat.py` reads that file. A library file is converted when the main track of one
of its languages (flagged default, else that language's first) is in a codec recorded there or in its seed (DTS and TrueHD, what it converted
before anything was measured), and no other track would play in its place: one in a playable codec,
the same language and as many channels up to 5.1, that is not a commentary. The recorded files go first, newest first, so the film that set off the alert is
the first one fixed. What it does **not** do:

- **Convert on a guess.** A codec no server has refused for a client yet is left alone.
- **Help a client that cannot play AC3.** AC3 is what it adds, so if AC3 itself is recorded as
  refused it says so in `audio-compat.log` and ignores it; the alert says the same.
- **Fix a client that turned direct play off.** That is in the client, not the file.
- **Pick a track for the client.** A file with a DTS 5.1 and an AC3 5.1 of the same mix already has
  something playable, so it is skipped, and if the client still chose the DTS the log says so the
  next night.
- **Forget.** A codec a client update fixed stays recorded until it is deleted from the JSON by hand,
  followed by `docker restart pi-metrics`: the collector holds the file in memory and would write the
  codec straight back.
- **Touch a file that is still seeding**, for the hardlink reason in the script's docstring. The
  page can, after a confirmation; [below](#a-file-that-is-still-seeding) is what that costs.

Adding a codec can put a lot of the library in the queue at once, and every conversion keeps the
old file as `.original` beside the new one, so the disk pays for both until those are pruned.
`RUN_SECONDS` spreads that out: the 01:30 pass converts one file after another and starts no new
one after 07:00, and each file still needs 40 GiB free after its copy.

## A file that is still seeding

Radarr imports by hardlink, so a film that is still seeding is the same inode as the torrent's file
(link count 2). The nightly pass leaves those alone. The page shows them as **still seeding
(hardlinked)** and converts one only after a confirmation, which passes `--allow-hardlinked`.

What that does: the old file is renamed to `.original`, so the inode keeps its second link and the
torrent's link count stays at 2. qbit-manage sees nothing change and the torrent keeps seeding. What
it costs is disk: both copies stay until the torrent is removed and `.original` deleted. Deleting
`.original` while the torrent still seeds frees nothing and drops its count to 1, which qbit-manage
reads as "the library let go" (`noHL`).

## One conversion at a time

Both modes of `audio-compat.py` take an `flock` on `appdata/audio-convert/convert.lock` and write who
holds it into that file. The page's queue runs one file at a time and waits while the lock is taken,
showing "The nightly pass is converting X". The nightly pass, if it finds the page converting
at 01:30, waits up to three hours for it and then gives up for the night, saying so in
`audio-compat.log`.

Every conversion either side attempts is appended to `appdata/audio-convert/history.jsonl`: which
file, from where, how long it took and at what speed, or why it failed. That file is the page's
history and the source of its speed average. The queue and the result of each file pressed on the
page are in `appdata/audio-convert/audio-convert.json`, so a restart keeps them. A conversion cut
short by a restart starts again from the beginning when the page comes back, since it is still at
the head of the queue; the script deletes its half-written file before it starts.

Before starting, both check free space the same way: what is left after a copy of the file must be
at least 40 GiB (`MIN_FREE_BYTES`). The page shows the free space and disables the button when a
file does not fit.

## What it does not do

- **Delete `.original` files.** Pruning those is a decision, not a timer, here as in the nightly pass.
- **Convert anything nobody listed as fixable.** The page only queues files whose ledger entry says
  `actionable` or that the last library scan found, and only by their path inside the library.
- **Forget.** An entry stays in the ledger until a hundred newer ones push it out. A file already
  converted shows as done; if a client then has it re-encoded again, it is listed again with a
  fresh plan.
- **Pick a track for the client.** The AC3 built from the default track is marked default; one added
  for another language leaves the default where it was. A client that remembers another choice keeps it.
- **Overwrite an `.original`.** When one is already there, from an earlier conversion or a release
  Radarr has since replaced, the old file goes to `.original.2` and up.

## Running it by hand

```bash
# What the page runs, from the host
scripts/sync/audio-compat.py --file "/mnt/data/films/Some Film (2020)/Some Film.mkv"
# Machine-readable, as the page reads it: one JSON object per line
scripts/sync/audio-compat.py --file "/mnt/data/films/..." --progress
# Another codec for the added track: ac3 (default), eac3 or aac
scripts/sync/audio-compat.py --file "/mnt/data/films/..." --target eac3
# What the nightly pass would convert, as JSON, converting nothing
scripts/sync/audio-compat.py --scan
```

Exit code 0 is converted, 1 failed (the reason is on stderr, or in the `failed` event), 75 busy:
someone else holds the lock.
