# Uploading a film to NOBS

NOBS credits upload, and the cheapest upload there is a film already in the library that the site
does not have. `scripts/ops/nobs-upload.sh` uploads one, by hand, with **RawLoadrr**: the uploader of
the RaW Suite ([Singularity](https://codeberg.org/RawSmoke/Singularity)) that the NOBS upload guide
recommends, a fork of Upload-Assistant with its own NOBS module. Only RawLoadrr is used, none of the
rest of the suite.

```bash
scripts/ops/nobs-upload.sh "/mnt/data/films/<Film (Year)>/<file>.mkv" "<NOBS name>" <tmdb id>
```

Nothing uploads without a "y" typed at the end. Never automate it, and never run upstream's
`auto-upload.py`: that one uploads a whole folder without asking.

## When to upload

Only when the ratio asks for it: under the site-wide freeleech nothing downloaded counts, and an
upload is effort and risk spent on credit nobody needs yet. The two tracker alerts already cover
NOBS, "Almost no paid downloads left" (headroom under 10 GB) and "Ratio under the line the tracker
disables at" (under 0.4), and for NOBS both end by pointing here. Then:

```bash
scripts/ops/nobs-candidates.py
```

It lists the films NOBS would take, each with the `nobs-upload.sh` line to start from: a Castilian
audio track, not an upscale, and nothing on NOBS at the file's resolution (searched by IMDb id). It
opens no file, reading the tracks audio-manager already probed, and films whose `spa` track is not
titled Castellano are listed apart, to be heard first. New candidates come from Add audio and from
Radarr grabbing a release that already carries the dub.

## Before an upload

Upload only what the site would accept. From its rules (read 2026-11):

- **Castilian audio.** A film dubbed in Castilian must carry that dub; the original version is
  recommended beside it. Most releases from English-language sites have no Spanish track, so they
  cannot go up as they are; a file add-audio gave the dub to can, as a mux of its own. The script
  refuses a file without a `spa` track, and one whose `spa` track does not say Castellano in its
  title until you listen to it and pass `CASTILIAN_OK=1` (a Latin-American dub is often tagged `spa`
  with no title). A film whose original language is Spanish is the same case: its track is often
  untitled.
- **Not already there**: the same film, type (encode, remux, WEB) and resolution is a duplicate
  unless clearly better. `scripts/ops/nobs-check.py` asks NOBS for every torrent with the film's
  TMDB id and stops when one has the file's resolution, whatever its type; `DUPE_OK=1` overrides it
  once you have compared them and yours is clearly better. RawLoadrr's own check is not enough: it
  judges by name similarity and size, and let a same-resolution WEB-DL through (2026-11-24, removed
  by the staff on request).
- **Not an internal of another private site.** The source group decides it: YTS, TGx and other
  public P2P groups are fine; a private site's internal group may not be shared. Check the group
  before uploading, RawLoadrr does not know.
- **The name**, always given by hand: RawLoadrr builds an English one in the international scene
  format, and NOBS wants the Spanish distribution title, then year, source, resolution, the codec and
  bit depth only when not the standard (x264 8-bit for HD, x265 10-bit for UHD), and each audio
  language with its codec, e.g. `El lobo de Wall Street 2013 BDRip 1080p x265 10Bit ESP AC3 ING EAC3`.
  The full rules are on the site's wiki, "Nombrado torrents".
- **Not upscaled**: a release titled UPSCALE is refused.

## What the script does

1. **Checks the raspi can take it**: the 5-minute load under `MAX_LOAD` (4), and the conversion lock
   that audio-compat and add-audio hold, so it never runs beside them.
2. **Hardlinks the file into `downloads/uploads/<release>/`**, a folder named like the file:
   RawLoadrr takes the title from the folder holding the file, and a file loose in `uploads/` was
   looked up as a film called "uploads" (it matched a 2021 series). qBittorrent does not mount the library, on
   purpose ([private-trackers.md](private-trackers.md)), so it cannot seed from `films/`. A hardlink
   is the same bytes under a second name and costs no space. It is never a copy: when the link
   fails (the pool put `uploads/` on another disk), the script stops.
3. **Checks audio and duplicates** (`nobs-check.py`), as above: ffprobe on the link, then NOBS's
   search API with the TMDB id. It lists the file's audio tracks and what NOBS has of the film.
4. **Dry run** (`--debug`): RawLoadrr reads the file with mediainfo, finds the film on TMDB, takes
   four screenshots and uploads them to pixhost, builds the .torrent and the description, and checks
   NOBS for duplicates. With `--debug` its NOBS module shows what it would send and sends nothing.
   Both runs pass `--unattended`, so RawLoadrr asks nothing itself (a possible duplicate is skipped,
   not asked about) and the only question is the script's own. Its stdin is `/dev/null`: ffmpeg
   reads stdin and swallowed the answers typed for RawLoadrr's prompts.
5. **Asks.** Anything but `y` stops there and removes the hardlink.
6. **The upload**, the same run without `--debug`. RawLoadrr then downloads the site's own .torrent
   (the one with your passkey, which is what the site asks to be seeded) and hands it to
   qBittorrent with the file's path, so it starts complete after a recheck.

The torrent lands in category `manual` with tags `upload` and `keep`. `keep` is skipped by every
share-limit group in qbit-manage, so an upload seeds until removed by hand, even after Radarr
replaces the library copy. Removing it is then two steps: the torrent in qBittorrent, then its file
in `downloads/uploads/`, which qbit-manage's orphan sweep leaves alone (it excludes the folder, since
a hardlink keeps the film's old date and the age check would not protect one being hashed).

## What it costs the raspi

Nothing between uploads: it is a `compose run`, not a running service, and its profile is not in
`all`, so the deploy neither starts nor pulls it. One upload:

- **Hashing** the file to build the .torrent reads the whole film once, and qBittorrent's recheck
  reads it again. That disk read is the heavy part.
- **Four screenshots**, one ffmpeg seek each; slower on 4K HDR.
- The dry run and the upload each do both, so an upload reads the film about three times.

It runs at `nice 19` and `ionice -c3` inside a 1 GB container with low CPU shares. Prefer a quiet
moment: not while something plays, not beside a big download.

## Configuration

| What | Where |
| :--- | :--- |
| RawLoadrr's config | `config/rawloadrr/config.py`, read as Python, secrets from the environment. Only NOBS is configured; the modules for the other sites stay inert without an entry |
| Secrets | `NOBS_APIKEY` (already used by tracker-control), `NOBS_PASSKEY`, `TMDB_API_KEY`, in homelab-secrets |
| Its .torrent files, screenshots and logs | `appdata/rawloadrr/` |
| The code | `services/rawloadrr/Dockerfile` builds one commit, `RAWLOADRR_COMMIT` in `versions.env` |

**Moving `RAWLOADRR_COMMIT` means reading the diff first.** The repository has no releases and
RawLoadrr talks to the site with your API key, so it has no Renovate line on purpose. The commit
pinned was reviewed on 2026-11-24: no telemetry or self-update; it talks to NOBS, TMDB, IMDb,
pixhost and qBittorrent. Two behaviours to know, both upstream's: without `--debug` it uploads
without asking, and `--debug` is a dry run that never uploads, which is why the script runs it twice.

The TMDB id is required: it is what the dupe check searches by, and RawLoadrr, run unattended,
skips a film whose match it cannot confirm. When it still asks for the IMDb id, pass
`--imdb tt<id>` after the TMDB id.
