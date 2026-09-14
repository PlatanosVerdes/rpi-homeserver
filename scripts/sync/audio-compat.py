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

A file that is still hardlinked is left alone, and that is the whole reason this runs on a timer
instead of on import. A remux cannot edit the file in place, so the new copy never shares an inode
with the one qBittorrent seeds, and the link count drops to 1. Two things read that count as "the
library has let go": qbit-manage's tag_nohardlinks, whose noHL tag is what admits a torrent into
every tracker group in its config, and those groups carry cleanup: true; and seed-cleanup.py, which
asks the same question first. seed-cleanup.py survives it, because its second question asks the arr
whether the imported path is still on disk, the same fallback that covers unpackerr's extracted
RARs. qbit-manage has no such fallback: it would read a remux as a film the library dropped and
start counting that torrent towards deletion, on a private tracker, for a reason that is not true.

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
from pathlib import Path

PROJECT_DIR = Path(os.path.expanduser("~/rpi-homeserver"))
DATA_ROOT = Path(os.environ.get("DATA_ROOT", "/mnt/data"))
LIBRARIES = ("films", "tv")
EXTENSIONS = (".mkv", ".mp4", ".m4v")

# What the TV decodes without help. TrueHD and DTS in every flavour are deliberately absent.
COMPATIBLE = {"ac3", "eac3", "aac", "mp3", "flac", "opus", "vorbis",
              "pcm_s16le", "pcm_s24le", "pcm_dvd"}

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


def needs_work(info):
    tracks = audio_streams(info)
    return bool(tracks) and not any(t.get("codec_name") in COMPATIBLE for t in tracks)


def default_audio_index(tracks):
    """Position among the audio streams of the track to convert: the one already marked default.

    That flag is what the release chose as its main mix, which on a foreign film is the original
    language rather than a dub, and on Perfect Blue picks the Original Mix over the later remix.
    """
    for position, track in enumerate(tracks):
        if track.get("disposition", {}).get("default"):
            return position
    return 0


def remux(src, dst, source_position, channels):
    """Prepend an AC3 track built from one existing track, copying everything else untouched."""
    cmd = [
        "nice", "-n", "10", "ionice", "-c", "3",
        "ffmpeg", "-nostdin", "-y", "-i", str(src),
        "-map", "0:v:0", "-map", f"0:a:{source_position}", "-map", "0:a",
        "-map", "0:s?", "-map", "0:t?",
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

    for kind in ("video", "audio", "subtitle"):
        was = sum(1 for s in src_info.get("streams", ()) if s.get("codec_type") == kind)
        now = sum(1 for s in info.get("streams", ()) if s.get("codec_type") == kind)
        expected = was + 1 if kind == "audio" else was
        if now != expected:
            return f"{kind} streams: {was} -> {now}, expected {expected}"

    if audio_streams(info)[0].get("codec_name") != "ac3":
        return "first audio track is not the new AC3"
    return None


def candidates():
    for library in LIBRARIES:
        root = DATA_ROOT / library
        if not root.is_dir():
            continue
        for path in sorted(root.rglob("*")):
            if path.suffix.lower() in EXTENSIONS and path.is_file():
                yield path


def main():
    done = 0
    deferred = []

    for path in candidates():
        if done >= MAX_PER_RUN:
            break

        info = probe(path)
        if info is None or not needs_work(info):
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

        failure = remux(path, tmp, position, channels) or verify(info, tmp)
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
