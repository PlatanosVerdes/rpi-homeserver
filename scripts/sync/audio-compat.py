#!/usr/bin/env python3
"""Give every media file an audio track the TV can decode on its own.

A file whose only audio is DTS forces Plex to build a DASH stream through ffmpeg instead of serving
the file untouched. That path throttles itself: Plex produces just ahead of playback and parks the
transcoder in sloth mode, so a 32 Mbit/s BluRay arrives with about 6% of headroom and every busy
scene empties the client buffer. Measured on Backrooms (2026): 34.11 Mbit/s delivered against
32.01 needed, the transcoder sitting at 0% CPU while the TV reported buffering, with the Pi 44%
idle and the disk reading at 147 MB/s. Nothing on the server is the bottleneck, so no amount of
server tuning fixes it.

Adding an AC3 5.1 track removes the transcode entirely. Every original track stays in the file, so
nothing is lost: the AC3 is only what plays by default.

Which codecs count as undecodable is learned, not listed. pi-metrics records every audio codec a
real client had re-encoded while its video was copied, and the file it happened on, in
appdata/pi-metrics/audio-recodes.json (why only those sessions: services/pi-metrics/recodes.go).
A file is converted when every audio track it has is in that set or in SEED. The files recorded
there go first, newest first, so the file that set off the alert is the first one the next run
fixes. A codec nobody has had re-encoded yet is left alone. The hand-kept list this replaced
counted EAC3 as playable, and the Plex app on a Samsung TV re-encodes it.

A file that is still hardlinked is left alone, and that is the whole reason this runs on a timer
instead of on import. A remux cannot edit the file in place, so the new copy never shares an inode
with the one qBittorrent seeds, and the link count drops to 1. qbit-manage reads that count as "the
library has let go": its tag_nohardlinks produces the noHL tag, that tag is what admits a torrent
into every tracker group in its config, and those groups carry cleanup: true. It has no second
question, so it would read a remux as a film the library dropped and start counting that torrent
towards deletion, on a private tracker, for a reason that is not true. Nothing else is watching
either: the script that used to ask the arrs what a download produced was retired once qbit-manage
took over deletion, which leaves that count the only signal there is.

So the seeded copy is never disturbed. Those films are fixed on a later pass, once the tracker's
term is served and the torrent released, which is also when remuxing stops costing a second copy
of the file.

The source is never modified in place. A new file is written beside it, checked against the
original for duration and for every stream it had, and only then does it take over the name. The
old file is kept as .original; pruning those is a decision, not a timer.

Silent unless something happened.
"""

import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
from itertools import chain
from pathlib import Path, PurePosixPath

PROJECT_DIR = Path(os.path.expanduser("~/rpi-homeserver"))
DATA_ROOT = Path(os.environ.get("DATA_ROOT", "/mnt/data"))
LIBRARIES = ("films", "series")
EXTENSIONS = (".mkv", ".mp4", ".m4v")

LEDGER = Path(os.environ.get("AUDIO_RECODES", PROJECT_DIR / "appdata" / "pi-metrics" / "audio-recodes.json"))

# Known before anything was measured, so a fresh install or an empty ledger converts what it always did.
SEED = {"dts", "truehd"}
# What this adds. A client that had it re-encoded cannot be helped by adding more of it.
TARGET = "ac3"
# The ledger keeps each server's own name for a codec; ffprobe has its own, and Plex says dca for DTS.
FFPROBE_NAMES = {"dca": "dts"}
# A recorded file that needs nothing is reported once, the night after it played, not every night.
RECENT_SECONDS = 24 * 3600

AC3_BITRATE = "640k"
AC3_MAX_CHANNELS = 6
MAX_PER_RUN = 2
FFPROBE_TIMEOUT = 60
# 20x realtime measured on this Pi, so a long film lands near ten minutes.
FFMPEG_TIMEOUT = 3600
MIN_FREE_BYTES = 40 * 1024**3
DURATION_TOLERANCE = 1.0


def probe(path):
    """Streams and format of a file, or None if ffprobe cannot read it."""
    try:
        out = subprocess.run(
            ["ffprobe", "-v", "error", "-print_format", "json",
             "-show_format", "-show_streams", str(path)],
            capture_output=True, text=True, timeout=FFPROBE_TIMEOUT, check=True).stdout
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired, OSError):
        return None
    try:
        return json.loads(out)
    except json.JSONDecodeError:
        return None


def audio_streams(info):
    return [s for s in info.get("streams", ()) if s.get("codec_type") == "audio"]


def ffprobe_name(codec):
    base = codec.lower().split("-")[0]
    return FFPROBE_NAMES.get(base, base)


def load_ledger():
    try:
        return json.loads(LEDGER.read_text())
    except FileNotFoundError:
        return {}
    except (OSError, json.JSONDecodeError) as err:
        print(f"{LEDGER}: unreadable ({err}), only {sorted(SEED)} count as incompatible", file=sys.stderr)
        return {}


def incompatible_codecs(ledger):
    learned = {}
    for codec, seen in (ledger.get("codecs") or {}).items():
        learned[ffprobe_name(codec)] = seen
    if TARGET in learned:
        clients = ", ".join(learned.pop(TARGET).get("clients") or ["?"])
        print(f"{TARGET} was re-encoded for {clients}: adding {TARGET} cannot help there, "
              f"so it is not treated as incompatible", file=sys.stderr)
    return SEED | set(learned)


def library_path(reported):
    """The host path of a file as a media server reported it, from the library folder down."""
    parts = PurePosixPath(reported).parts
    for position, part in enumerate(parts):
        if part in LIBRARIES:
            return DATA_ROOT.joinpath(*parts[position:])
    return None


def reported_files(ledger):
    out = {}
    for entry in ledger.get("files") or ():
        path = library_path(entry.get("path") or "")
        if path is not None and path not in out:
            out[path] = entry
    return out


def needs_work(info, incompatible):
    tracks = audio_streams(info)
    return bool(tracks) and all(t.get("codec_name") in incompatible for t in tracks)


def default_audio_index(tracks):
    """Position among the audio streams of the track to convert: the one already marked default.

    That flag is what the release chose as its main mix, which on a foreign film is the original
    language rather than a dub, and on Perfect Blue picks the Original Mix over the later remix.
    """
    for position, track in enumerate(tracks):
        if track.get("disposition", {}).get("default"):
            return position
    return 0


def is_cover(stream):
    return stream.get("codec_type") == "video" and bool(stream.get("disposition", {}).get("attached_pic"))


def tag(stream, name):
    for key, value in (stream.get("tags") or {}).items():
        if key.lower() == name:
            return value
    return None


def cover_args(src, info, dst, workdir):
    """The arguments that carry every cover across as a cover, or a failure."""
    covers = [s for s in info.get("streams", ()) if is_cover(s)]
    if dst.suffix.lower() != ".mkv":
        return [arg for s in covers for arg in ("-map", f"0:{s['index']}")], None

    # Matroska keeps a cover as an attachment. ffmpeg reads it back as a video stream and, mapped
    # as one, writes a one-frame video track instead, so it is extracted and attached again.
    attachments = sum(1 for s in info.get("streams", ()) if s.get("codec_type") == "attachment")
    args = []
    for position, stream in enumerate(covers):
        name = tag(stream, "filename") or f"cover{position}"
        image = Path(workdir) / f"{position}-{Path(name).name}"
        try:
            subprocess.run(
                ["ffmpeg", "-nostdin", "-y", "-v", "error", "-i", str(src),
                 "-map", f"0:{stream['index']}", "-c", "copy", "-frames:v", "1",
                 "-f", "image2", str(image)],
                capture_output=True, text=True, timeout=FFPROBE_TIMEOUT, check=True)
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired, OSError) as err:
            return None, f"cover {name} could not be extracted: {err}"
        output = attachments + position
        args += ["-attach", str(image), f"-metadata:s:t:{output}", f"filename={name}"]
        if tag(stream, "mimetype"):
            args += [f"-metadata:s:t:{output}", f"mimetype={tag(stream, 'mimetype')}"]
    return args, None


def remux(src, dst, info, source_position, channels, workdir):
    """Prepend an AC3 track built from one existing track, copying everything else untouched."""
    covers, failure = cover_args(src, info, dst, workdir)
    if failure:
        return failure
    cmd = [
        "nice", "-n", "10", "ionice", "-c", "3",
        "ffmpeg", "-nostdin", "-y", "-i", str(src),
        "-map", "0:V", "-map", f"0:a:{source_position}", "-map", "0:a",
        "-map", "0:s?", "-map", "0:t?", *covers,
        "-c:v", "copy", "-c:s", "copy", "-c:t", "copy", "-c:a", "copy",
        "-c:a:0", "ac3", "-b:a:0", AC3_BITRATE,
    ]
    if channels > AC3_MAX_CHANNELS:
        cmd += ["-ac:a:0", str(AC3_MAX_CHANNELS)]
    cmd += [
        "-disposition:a", "0", "-disposition:a:0", "default",
        "-metadata:s:a:0", "title=AC3 5.1 (compatible TV)",
        str(dst),
    ]
    try:
        done = subprocess.run(cmd, capture_output=True, text=True, timeout=FFMPEG_TIMEOUT)
    except (subprocess.TimeoutExpired, OSError) as err:
        return f"ffmpeg did not finish: {err}"
    if done.returncode != 0:
        tail = done.stderr.strip().splitlines()[-1:] or ["no output"]
        return f"ffmpeg exited {done.returncode}: {tail[0]}"
    return None


def kind(stream):
    return "cover" if is_cover(stream) else stream.get("codec_type")


def verify(src_info, dst):
    """Reject the new file unless it carries the whole of the old one plus one audio track."""
    info = probe(dst)
    if info is None:
        return "new file is unreadable"

    try:
        before = float(src_info["format"]["duration"])
        after = float(info["format"]["duration"])
    except (KeyError, TypeError, ValueError):
        return "duration missing"
    if abs(after - before) > DURATION_TOLERANCE:
        return f"duration moved: {before:.3f} -> {after:.3f}"

    for wanted in ("video", "cover", "audio", "subtitle"):
        was = sum(1 for s in src_info.get("streams", ()) if kind(s) == wanted)
        now = sum(1 for s in info.get("streams", ()) if kind(s) == wanted)
        expected = was + 1 if wanted == "audio" else was
        if now != expected:
            return f"{wanted} streams: {was} -> {now}, expected {expected}"

    if audio_streams(info)[0].get("codec_name") != "ac3":
        return "first audio track is not the new AC3"
    return None


def candidates():
    for library in LIBRARIES:
        root = DATA_ROOT / library
        # Loud: this script is silent when it finds nothing, so a library skipped for being
        # misnamed reads exactly like a library with nothing to convert.
        if not root.is_dir():
            print(f"library {root} does not exist, nothing from it was checked", file=sys.stderr)
            continue
        for path in sorted(root.rglob("*")):
            if path.suffix.lower() in EXTENSIONS and path.is_file():
                yield path


def main():
    ledger = load_ledger()
    incompatible = incompatible_codecs(ledger)
    reported = reported_files(ledger)
    now = time.time()
    done = 0
    deferred = []

    queue = chain((p for p in reported if p.is_file()), (p for p in candidates() if p not in reported))
    for path in queue:
        if done >= MAX_PER_RUN:
            break

        info = probe(path)
        if info is None:
            continue
        if not needs_work(info, incompatible):
            entry = reported.get(path)
            if entry and now - (entry.get("seen") or 0) < RECENT_SECONDS:
                kept = sorted({t.get("codec_name") for t in audio_streams(info)} - incompatible)
                print(f"{path.name}: {entry.get('app')} re-encoded its {entry.get('codec')} for "
                      f"{entry.get('client')}, but it also carries {', '.join(kept) or 'no audio'}; "
                      f"nothing to add, the client played another track or cannot play those either")
            continue

        if path.stat().st_nlink > 1:
            deferred.append(path.name)
            continue

        size = path.stat().st_size
        if shutil.disk_usage(path.parent).free - size < MIN_FREE_BYTES:
            print(f"{path.name}: skipped, not enough free space", file=sys.stderr)
            continue

        tracks = audio_streams(info)
        position = default_audio_index(tracks)
        channels = int(tracks[position].get("channels") or AC3_MAX_CHANNELS)
        tmp = path.with_name(".audio-compat.in-progress" + path.suffix)
        tmp.unlink(missing_ok=True)

        with tempfile.TemporaryDirectory(dir=path.parent, prefix=".audio-compat.") as workdir:
            failure = remux(path, tmp, info, position, channels, workdir) or verify(info, tmp)
        if failure:
            tmp.unlink(missing_ok=True)
            print(f"{path.name}: {failure}", file=sys.stderr)
            continue

        path.rename(path.with_name(path.name + ".original"))
        tmp.rename(path)
        print(f"{path.name}: added AC3 from track {position}, old file kept as .original")
        done += 1

    if deferred:
        print(f"still seeding, left for a later pass: {', '.join(sorted(deferred))}")


if __name__ == "__main__":
    main()
