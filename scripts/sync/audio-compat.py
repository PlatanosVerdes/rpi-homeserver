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

Which codecs count as undecodable is learned, not listed. pi-metrics records every audio codec the
server refused for a real client that had allowed direct play, and the file it happened on, in
appdata/pi-metrics/audio-recodes.json. A re-encode alone is not that: a client that turns direct
play off gets a fallback that takes only AAC, and every other codec is re-encoded, the AC3 this adds
included (services/pi-metrics/cause.go and recodes.go).
Every language is looked at, not only the default one. Its main track is the one flagged default
when that is in the language, else its first track that is not a commentary. A file is converted
when one of those main tracks is in that set or in SEED, and nothing else in it would play in that
track's place: a compatible track in the same language with as many channels, up to 5.1, and not a
commentary. A stereo commentary or an MP3 beside a DTS-X main mix is not what the TV plays, so it
does not count. A MULTi release with French and English both in TrueHD gets one AC3 per language:
built from the default alone, picking English on the TV silently played the French AC3. The files recorded
there go first, newest first, so the file that set off the alert is the first one the next run
fixes. A codec no server has refused for a client yet is left alone. The hand-kept list this replaced
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
old file is kept as .original; pruning those is a decision, not a timer. When that name is taken,
by a file converted before or by a release Radarr since replaced, it goes to .original.2 and up:
nothing is ever renamed over.

Silent unless something happened.

    audio-compat.py                       the nightly pass over the library
    audio-compat.py --file PATH [--progress] [--allow-hardlinked] [--track N ...]

`--file` converts one file through the same checks, remux, verify and rename, and is what
services/audio-convert runs when its Play button is pressed. `--progress` makes it print one JSON
object per line instead of prose: a plan, progress from ffmpeg's own -progress output, and a done or
failed with the reason. `--track`, repeatable, builds the AC3 from the audio tracks pi-metrics
planned, the one the client played among them, instead of the main track of every language. `--allow-hardlinked` is the page's explicit
"convert it anyway" for a file still seeding. The rename keeps the old inode under .original, so the torrent's link survives and
qbit-manage still counts 2; what it costs is the file's size again until the torrent is gone and
.original pruned.

Both modes take the same flock, in the state directory services/audio-convert mounts, so the night
run and the page never convert at once. Every conversion either of them attempts is appended to
history.jsonl there, which is how the page shows what the night did and measures the speed.
"""

import argparse
import fcntl
import json
import os
import shutil
import subprocess
import sys
import tempfile
import threading
import time
from contextlib import contextmanager
from itertools import chain
from pathlib import Path, PurePosixPath

PROJECT_DIR = Path(os.path.expanduser("~/rpi-homeserver"))
DATA_ROOT = Path(os.environ.get("DATA_ROOT", "/mnt/data"))
LIBRARIES = ("films", "series")
EXTENSIONS = (".mkv", ".mp4", ".m4v")

LEDGER = Path(os.environ.get("AUDIO_RECODES", PROJECT_DIR / "appdata" / "pi-metrics" / "audio-recodes.json"))
STATE_DIR = Path(os.environ.get("AUDIO_CONVERT_STATE", PROJECT_DIR / "appdata" / "audio-convert"))
LOCK = STATE_DIR / "convert.lock"
HISTORY = STATE_DIR / "history.jsonl"

# Known before anything was measured, so a fresh install or an empty ledger converts what it always did.
SEED = {"dts", "truehd"}
# What this adds. A client that had it re-encoded cannot be helped by adding more of it.
TARGET = "ac3"
# The ledger keeps each server's own name for a codec; ffprobe has its own, and Plex says dca for DTS.
FFPROBE_NAMES = {"dca": "dts"}
# A recorded file that needs nothing is reported once, the night after it played, not every night.
RECENT_SECONDS = 24 * 3600

# What --target can ask for. The nightly pass always adds TARGET: AC3 is the one every TV decodes.
TARGETS = {
    "ac3": {"codec": "ac3", "bitrate": "640k", "max_channels": 6},
    "eac3": {"codec": "eac3", "bitrate": "640k", "max_channels": 6},
    "aac": {"codec": "aac", "bitrate": "256k", "max_channels": 2},
}
AC3_BITRATE = TARGETS["ac3"]["bitrate"]
AC3_MAX_CHANNELS = TARGETS["ac3"]["max_channels"]
LAYOUTS = {1: "mono", 2: "stereo", 6: "5.1", 8: "7.1"}
# Cron starts the nightly pass at 01:30; no conversion starts after 07:00. One takes about ten minutes.
RUN_SECONDS = int(5.5 * 3600)
FFPROBE_TIMEOUT = 60
# 20x realtime measured on this Pi, so a long film lands near ten minutes.
FFMPEG_TIMEOUT = 3600
MIN_FREE_BYTES = 40 * 1024**3
DURATION_TOLERANCE = 1.0
# The page converts one film at a time, ten minutes each, so a queue of a few clears well inside this.
LOCK_WAIT_SECONDS = 3 * 3600
EXIT_BUSY = 75


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
        # A client that turned direct play off is listed for the page to explain, not to convert.
        if entry.get("cause", "codec") != "codec":
            continue
        path = library_path(entry.get("path") or "")
        if path is not None and path not in out:
            out[path] = entry
    return out


def describe(track):
    words = [tag(track, "language"), (track.get("codec_name") or "?").upper()]
    if track.get("channels"):
        words.append(f"{track['channels']}ch")
    return " ".join(w for w in words if w)


def same_language(a, b):
    a, b = (tag(a, "language") or "und").lower(), (tag(b, "language") or "und").lower()
    return a == b or "und" in (a, b)


def stand_in(tracks, main, incompatible):
    """A compatible track that would play in the main track's place, or None."""
    wanted = min(int(main.get("channels") or AC3_MAX_CHANNELS), AC3_MAX_CHANNELS)
    for track in tracks:
        if track is main or track.get("codec_name") in incompatible:
            continue
        if track.get("disposition", {}).get("comment") or not same_language(track, main):
            continue
        if int(track.get("channels") or 0) >= wanted:
            return track
    return None


def language_key(track):
    return (tag(track, "language") or "und").lower()


def main_tracks(tracks):
    """Position of the main track of each language, the default one's language first."""
    lead = default_audio_index(tracks)
    mains = {language_key(tracks[lead]): lead}
    for position, track in enumerate(tracks):
        if not track.get("disposition", {}).get("comment"):
            mains.setdefault(language_key(track), position)
    return list(mains.values())


def assess(info, incompatible, positions=None):
    """The audio positions to build an AC3 from, and why not when there is nothing to do.

    The same rule is in services/pi-metrics/plan.go; change both together.
    """
    tracks = audio_streams(info)
    if not tracks:
        return [], "it has no audio"
    for position in positions or ():
        if not 0 <= position < len(tracks):
            return [], f"it has no audio track {position}"
    needed, reasons = [], []
    for position in positions or main_tracks(tracks):
        main = tracks[position]
        other = stand_in(tracks, main, incompatible)
        if main.get("codec_name") not in incompatible:
            reasons.append(f"its main track is {describe(main)}, a codec no client has been refused")
        elif other is not None:
            reasons.append(f"it already has {describe(other)}, which plays in place of the {describe(main)}")
        else:
            needed.append(position)
    if needed:
        return needed, None
    return [], reasons[0]


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


def frame_rate(info):
    """Frames per second of the main video stream, or None."""
    for stream in info.get("streams", ()):
        if stream.get("codec_type") == "video" and not is_cover(stream):
            num, _, den = (stream.get("avg_frame_rate") or "0/0").partition("/")
            try:
                return float(num) / float(den) or None
            except (ValueError, ZeroDivisionError):
                return None
    return None


def progress_seconds(fields, fps):
    """How far into the film ffmpeg's -progress block says it is, in seconds, or None.

    out_time follows the stream furthest behind, and a subtitle track with one cue at minute two
    holds it there for the whole film on ffmpeg 8, so the video frame count is asked as well.
    """
    found = []
    # out_time_ms is in microseconds too, a long-standing ffmpeg misnomer; out_time_us is newer.
    for key in ("out_time_us", "out_time_ms"):
        try:
            found.append(int(fields[key]) / 1e6)
            break
        except (KeyError, ValueError):
            continue
    try:
        if fps:
            found.append(int(fields["frame"]) / fps)
    except (KeyError, ValueError):
        pass
    return max(found) if found else None


def run_ffmpeg(cmd, on_progress):
    """Run ffmpeg with -progress on stdout, calling on_progress(fields) once per block."""
    timed_out = threading.Event()
    with tempfile.TemporaryFile(mode="w+") as stderr:
        try:
            proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=stderr, text=True)
        except OSError as err:
            return f"ffmpeg did not start: {err}"
        timer = threading.Timer(FFMPEG_TIMEOUT, lambda: (timed_out.set(), proc.kill()))
        timer.start()
        try:
            fields = {}
            for line in proc.stdout:
                key, _, value = line.strip().partition("=")
                fields[key] = value
                if key == "progress":
                    if on_progress:
                        on_progress(fields)
                    fields = {}
            proc.wait()
        finally:
            timer.cancel()
        if timed_out.is_set():
            return f"ffmpeg did not finish in {FFMPEG_TIMEOUT}s"
        if proc.returncode != 0:
            stderr.seek(0)
            tail = stderr.read().strip().splitlines()[-1:] or ["no output"]
            return f"ffmpeg exited {proc.returncode}: {tail[0]}"
    return None


def target_title(target, channels):
    out = min(channels, TARGETS[target]["max_channels"])
    return f"{target.upper()} {LAYOUTS.get(out, f'{out}ch')} (compatible TV)"


def remux(src, dst, info, sources, workdir, on_progress=None, target=TARGET):
    """Prepend a target track built from each (position, channels) source, copying everything else untouched."""
    spec = TARGETS[target]
    covers, failure = cover_args(src, info, dst, workdir)
    if failure:
        return failure
    cmd = [
        "nice", "-n", "10", "ionice", "-c", "3",
        "ffmpeg", "-nostdin", "-y", "-nostats", "-progress", "pipe:1", "-i", str(src),
        "-map", "0:V", *(arg for position, _ in sources for arg in ("-map", f"0:a:{position}")), "-map", "0:a",
        "-map", "0:s?", "-map", "0:t?", *covers,
        "-c:v", "copy", "-c:s", "copy", "-c:t", "copy", "-c:a", "copy",
    ]
    for out, (_, channels) in enumerate(sources):
        cmd += [f"-c:a:{out}", spec["codec"], f"-b:a:{out}", spec["bitrate"],
                f"-metadata:s:a:{out}", f"title={target_title(target, channels)}"]
        if channels > spec["max_channels"]:
            cmd += [f"-ac:a:{out}", str(spec["max_channels"])]
    if sources[0][0] == default_audio_index(audio_streams(info)):
        cmd += ["-disposition:a", "0", "-disposition:a:0", "default"]
    else:
        # The default language already plays; the added ones must not take its place.
        cmd += [arg for out in range(len(sources)) for arg in (f"-disposition:a:{out}", "0")]
    cmd.append(str(dst))
    return run_ffmpeg(cmd, on_progress)


def kind(stream):
    return "cover" if is_cover(stream) else stream.get("codec_type")


def verify(src_info, dst, added, target=TARGET):
    """Reject the new file unless it carries the whole of the old one plus the added audio tracks."""
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
        expected = was + added if wanted == "audio" else was
        if now != expected:
            return f"{wanted} streams: {was} -> {now}, expected {expected}"

    if any(t.get("codec_name") != TARGETS[target]["codec"] for t in audio_streams(info)[:added]):
        return f"first {added} audio tracks are not the new {target.upper()}"
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


@contextmanager
def convert_lock(mode, wait):
    """The flock both the night run and the page take, or None when someone else holds it."""
    try:
        STATE_DIR.mkdir(parents=True, exist_ok=True)
        handle = open(LOCK, "a+")
    except OSError as err:
        raise SystemExit(f"{LOCK}: {err}; nothing converted")
    with handle:
        deadline = time.monotonic() + (LOCK_WAIT_SECONDS if wait else 0)
        while True:
            try:
                fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
                break
            except BlockingIOError:
                if time.monotonic() >= deadline:
                    yield None
                    return
                time.sleep(30)

        def holding(path):
            handle.seek(0)
            handle.truncate()
            handle.write(json.dumps({"mode": mode, "pid": os.getpid(), "file": str(path or ""),
                                     "since": int(time.time())}))
            handle.flush()

        holding(None)
        try:
            yield holding
        finally:
            handle.seek(0)
            handle.truncate()


def lock_holder():
    try:
        return json.loads(LOCK.read_text() or "{}")
    except (OSError, json.JSONDecodeError):
        return {}


def record(mode, path, outcome):
    """Append one attempted conversion to the history the page shows. Called with the lock held."""
    entry = {"time": int(time.time()), "mode": mode, "path": str(path), "name": path.name, **outcome}
    try:
        with open(HISTORY, "a") as handle:
            handle.write(json.dumps(entry) + "\n")
    except OSError as err:
        print(f"{HISTORY}: {err}", file=sys.stderr)


def free_original_name(path):
    """.original, or .original.2 and up when an earlier conversion or release already holds that name."""
    candidate, n = path.with_name(path.name + ".original"), 1
    while candidate.exists():
        n += 1
        candidate = path.with_name(f"{path.name}.original.{n}")
    return candidate


def convert(path, info, positions, mode, on_progress=None, target=TARGET):
    """Remux, verify and swap one file in. Returns the outcome written to the history."""
    tracks = audio_streams(info)
    source = tracks[positions[0]]
    sources = [(p, int(tracks[p].get("channels") or AC3_MAX_CHANNELS)) for p in positions]
    duration = float(info.get("format", {}).get("duration") or 0)
    tmp = path.with_name(".audio-compat.in-progress" + path.suffix)
    tmp.unlink(missing_ok=True)

    started = time.monotonic()
    with tempfile.TemporaryDirectory(dir=path.parent, prefix=".audio-compat.") as workdir:
        failure = (remux(path, tmp, info, sources, workdir, on_progress, target)
                   or verify(info, tmp, len(sources), target))
    seconds = time.monotonic() - started
    outcome = {"source_position": positions[0], "source_positions": positions, "source_codec": source.get("codec_name"), "target": target,
               "duration": duration, "seconds": round(seconds, 1),
               "speed": round(duration / seconds, 2) if seconds > 0 and duration > 0 else None}
    if failure:
        tmp.unlink(missing_ok=True)
        record(mode, path, {"result": "failed", "reason": failure, **outcome})
        return failure, outcome

    path.rename(free_original_name(path))
    tmp.rename(path)
    record(mode, path, {"result": "done", **outcome})
    return None, outcome


def main():
    ledger = load_ledger()
    incompatible = incompatible_codecs(ledger)
    reported = reported_files(ledger)
    now = time.time()
    deadline = time.monotonic() + RUN_SECONDS
    done = 0
    deferred = []

    with convert_lock("nightly", wait=True) as holding:
        if holding is None:
            holder = lock_holder()
            print(f"another conversion held the lock for {LOCK_WAIT_SECONDS}s "
                  f"({holder.get('mode', '?')}: {holder.get('file', '?')}), nothing converted tonight",
                  file=sys.stderr)
            return

        queue = chain((p for p in reported if p.is_file()), (p for p in candidates() if p not in reported))
        for path in queue:
            if time.monotonic() >= deadline:
                print(f"stopped after {done} at the end of the night window, the rest is left for tomorrow")
                break

            info = probe(path)
            if info is None:
                continue
            positions, why_not = assess(info, incompatible)
            if why_not:
                entry = reported.get(path)
                if entry and now - (entry.get("seen") or 0) < RECENT_SECONDS:
                    print(f"{path.name}: {entry.get('app')} re-encoded its {entry.get('codec')} for "
                          f"{entry.get('client')}, but {why_not}; nothing to add, the client played "
                          f"another track or cannot play that one either")
                continue

            if path.stat().st_nlink > 1:
                deferred.append(path.name)
                continue

            size = path.stat().st_size
            if shutil.disk_usage(path.parent).free - size < MIN_FREE_BYTES:
                print(f"{path.name}: skipped, not enough free space", file=sys.stderr)
                continue

            holding(path)
            failure, outcome = convert(path, info, positions, "nightly")
            holding(None)
            if failure:
                print(f"{path.name}: {failure}", file=sys.stderr)
                continue
            print(f"{path.name}: added AC3 from {tracks_words(positions)}, .original kept")
            done += 1

    if deferred:
        print(f"still seeding, left for a later pass: {', '.join(sorted(deferred))}")


class Reporter:
    """Prose on stdout/stderr for a person, or one JSON object per line for services/audio-convert."""

    def __init__(self, machine):
        self.machine = machine
        self.last = 0.0

    def emit(self, event, **fields):
        if self.machine:
            print(json.dumps({"event": event, **fields}), flush=True)

    def progress(self, duration, fps):
        started = time.monotonic()

        def report(fields):
            seconds = progress_seconds(fields, fps)
            now = time.monotonic()
            if seconds is None or now - self.last < 1:
                return
            self.last = now
            # Measured here: ffmpeg's own speed= divides the same out_time that can stall.
            speed = seconds / (now - started) if now > started else None
            self.emit("progress", seconds=round(seconds, 1), duration=duration,
                      speed=round(speed, 2) if speed else None)
        return report

    def failed(self, path, reason, code=1):
        if self.machine:
            self.emit("failed", reason=reason)
        else:
            print(f"{path.name}: {reason}", file=sys.stderr)
        return code


def resolve(given):
    path = Path(given)
    if path.is_file():
        return path
    return library_path(given) or path


def tracks_words(positions):
    return f"track{'s' if len(positions) > 1 else ''} {', '.join(map(str, positions))}"


def source_fields(track, position):
    return {"position": position, "index": track.get("index"), "codec": track.get("codec_name"),
            "channels": track.get("channels"), "language": tag(track, "language")}


def convert_file(given, machine, allow_hardlinked, tracks=None, target=TARGET):
    out = Reporter(machine)
    path = resolve(given)
    if not path.is_file():
        return out.failed(path, "no such file")
    if path.suffix.lower() not in EXTENSIONS:
        return out.failed(path, f"not a {', '.join(EXTENSIONS)} file")

    with convert_lock("app", wait=False) as holding:
        if holding is None:
            holder = lock_holder()
            return out.failed(path, f"busy: the {holder.get('mode', '?')} run is converting "
                                    f"{Path(holder.get('file') or '?').name}", EXIT_BUSY)

        info = probe(path)
        if info is None:
            return out.failed(path, "ffprobe cannot read it")
        incompatible = incompatible_codecs(load_ledger())
        positions, why_not = assess(info, incompatible, tracks)
        if why_not:
            return out.failed(path, f"nothing to add: {why_not}")

        stat = path.stat()
        if stat.st_nlink > 1 and not allow_hardlinked:
            return out.failed(path, "still seeding (hardlinked); --allow-hardlinked converts it anyway")
        if shutil.disk_usage(path.parent).free - stat.st_size < MIN_FREE_BYTES:
            return out.failed(path, f"not enough free space: it needs {MIN_FREE_BYTES // 1024**3} GiB "
                                    f"left after a copy of the file")

        audio = audio_streams(info)
        sources = [source_fields(audio[p], p) for p in positions]
        duration = float(info.get("format", {}).get("duration") or 0)
        out.emit("plan", path=str(path), duration=duration, size=stat.st_size, links=stat.st_nlink,
                 source=sources[0], sources=sources, target=target)
        holding(path)
        failure, outcome = convert(path, info, positions, "app", out.progress(duration, frame_rate(info)), target)
    if failure:
        return out.failed(path, failure)
    if machine:
        out.emit("done", **outcome)
    else:
        print(f"{path.name}: added {target.upper()} from {tracks_words(positions)}, .original kept")
    return 0


def scan():
    """Every library file the nightly pass would convert, shaped like a pi-metrics ledger entry.

    services/audio-convert lists these under the files a client actually had re-encoded, so the
    rule that picks them stays here and the page never has a second copy of it.
    """
    incompatible = incompatible_codecs(load_ledger())
    bitrate = int(AC3_BITRATE.rstrip("k"))
    found = []
    for path in candidates():
        info = probe(path)
        if info is None:
            continue
        positions, why_not = assess(info, incompatible)
        if why_not:
            continue
        audio = audio_streams(info)
        main = audio[positions[0]]
        duration = float(info.get("format", {}).get("duration") or 0)
        relative = path.relative_to(DATA_ROOT)
        title = path.parent.name if relative.parts[0] == "films" else path.stem
        sources = [{"position": p, "codec": audio[p].get("codec_name"), "language": tag(audio[p], "language"),
                    "channels": int(audio[p].get("channels") or AC3_MAX_CHANNELS),
                    "default": bool(audio[p].get("disposition", {}).get("default")),
                    "title": tag(audio[p], "title") or ""} for p in positions]
        found.append({
            "path": str(path), "library_path": relative.as_posix(), "title": title,
            "codec": main.get("codec_name"), "cause": "library", "actionable": True, "duration": duration,
            "plan": {
                "source": sources[0], "sources": sources,
                "target_codec": TARGET, "target_bitrate_kbps": bitrate,
                "target_channels": min(sources[0]["channels"], AC3_MAX_CHANNELS),
                "extra_bytes": int(bitrate * 1000 / 8 * duration) * len(sources),
            },
        })
    targets = {name: {"codec": spec["codec"], "bitrate_kbps": int(spec["bitrate"].rstrip("k")),
                      "max_channels": spec["max_channels"]} for name, spec in TARGETS.items()}
    json.dump({"time": int(time.time()), "files": found, "targets": targets, "default_target": TARGET}, sys.stdout)
    print()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Add an AC3 track to files a client could not decode.")
    parser.add_argument("--file", help="convert this one file instead of the nightly pass")
    parser.add_argument("--progress", action="store_true", help="with --file: JSON lines on stdout")
    parser.add_argument("--allow-hardlinked", action="store_true", help="with --file: convert a seeding file")
    parser.add_argument("--target", choices=sorted(TARGETS), default=TARGET,
                        help="with --file: the codec of the added track (default %(default)s)")
    parser.add_argument("--scan", action="store_true", help="list, as JSON, the files the nightly pass would "
                                                              "convert; converts nothing")
    parser.add_argument("--track", type=int, action="append", help="with --file, repeatable: an audio track "
                                                                    "(0:a:N) to build from, instead of the main "
                                                                    "track of every language")
    args = parser.parse_args()
    if args.scan:
        if args.file:
            parser.error("--scan lists the whole library; it does not take --file")
        sys.exit(scan())
    if args.file:
        sys.exit(convert_file(args.file, args.progress, args.allow_hardlinked, args.track, args.target))
    if args.progress or args.allow_hardlinked or args.track is not None or args.target != TARGET:
        parser.error("--progress, --allow-hardlinked, --track and --target only apply with --file")
    main()
