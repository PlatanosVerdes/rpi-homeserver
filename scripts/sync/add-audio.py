#!/usr/bin/env python3
"""Add a dubbed audio track from another release of the same film, synced to the library file.

    add-audio.py TARGET SOURCE [--lang spa] [--track N] [--default] [--dry-run]

The sync is measured, not assumed: both files share music and effects, so their loudness envelopes
line up once the source is mapped as t_source = a * t_target + b. `a` covers a PAL speed-up or any
drift, `b` the offset; both are fitted over windows across the whole film, and a source whose
windows disagree (another cut, missing scenes) is refused rather than muxed half in sync. When the
source also carries the target's own language, that track is what gets aligned: identical audio
lines up far more sharply than a dub does.

The added track is always AC3, the one codec every TV here plays without Plex re-encoding it: an
AC3 dub at the same speed is copied untouched, anything else is converted to AC3 640k, up to 5.1.
It goes in with mkvmerge next to the original tracks, the result is checked against the dub it came
from, and the old file is kept as .original, through the same lock and history as audio-compat.py.

It runs in its own systemd scope capped at 1 GB, page cache included, at the lowest CPU and I/O
priority: decoding a film twice and writing a 10 GB copy once pushed every other service into swap.
"""

import argparse
import importlib.util
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path

import numpy as np

spec = importlib.util.spec_from_file_location("audio_compat", Path(__file__).with_name("audio-compat.py"))
compat = importlib.util.module_from_spec(spec)
spec.loader.exec_module(compat)

RATE = 8000
HOP = 40
FPS = RATE / HOP
SPEED_CANDIDATES = (1.0, 23.976 / 25, 25 / 23.976, 24 / 25, 25 / 24, 23.976 / 24, 24 / 23.976)
MAX_OFFSET_SECONDS = 600
WINDOW_SECONDS = 120
WINDOWS = 16
LOCAL_SEARCH_SECONDS = 5
MIN_PEAK_QUALITY = 6.0
MIN_GOOD_WINDOWS = 0.6
MAX_RESIDUAL_SECONDS = 0.045
SAME_SPEED = 2e-5
TARGET_CODEC = "ac3"
LANGUAGE_ALIASES = {"spa": {"spa", "es", "esp", "es-es"}}
LANGUAGE_TAGS = {"spa": "es-ES"}
TRACK_NAMES = {"spa": "Castellano"}
REJECTED_TITLES = {"spa": re.compile(r"latin|\blat\b|m[eé]x|neutr|commentary|comentario", re.I)}
PREFERRED_TITLES = {"spa": re.compile(r"castellano|espa[nñ]a|\bcast\b|europe", re.I)}
ANY_COMMENTARY = re.compile(r"commentary|comentario", re.I)
SCOPE = ["systemd-run", "--user", "--scope", "--quiet", "-p", "MemoryHigh=700M", "-p", "MemoryMax=1G",
         "-p", "MemorySwapMax=0", "-p", "CPUWeight=1"]
SCOPED_ENV = "ADD_AUDIO_SCOPED"


def language(stream):
    return (stream.get("tags", {}).get("language") or "").lower()


def title(stream):
    return stream.get("tags", {}).get("title") or ""


def start_time(stream):
    return float(stream.get("start_time") or 0)


def describe(stream):
    return (f"#{stream['index']} {stream.get('codec_name')} {stream.get('channels')}ch "
            f"{language(stream) or '?'} {title(stream)!r}")


def pick_dub(info, lang, forced=None):
    tracks = compat.audio_streams(info)
    if forced is not None:
        found = [s for s in tracks if s["index"] == forced]
        return found[0] if found else None
    aliases = LANGUAGE_ALIASES.get(lang, {lang})
    rejected = REJECTED_TITLES.get(lang, ANY_COMMENTARY)
    preferred = PREFERRED_TITLES.get(lang)
    matches = [s for s in tracks if language(s) in aliases and not rejected.search(title(s))]
    if not matches:
        return None
    return max(matches, key=lambda s: (bool(preferred and preferred.search(title(s))),
                                       s.get("codec_name") == TARGET_CODEC,
                                       int(s.get("channels") or 0)))


def main_track(info):
    tracks = compat.audio_streams(info)
    defaults = [s for s in tracks if s.get("disposition", {}).get("default")]
    return (defaults or tracks or [None])[0]


def pick_reference(info, dub, target_main):
    wanted = language(target_main)
    for stream in compat.audio_streams(info):
        if stream is not dub and wanted and language(stream) == wanted and not ANY_COMMENTARY.search(title(stream)):
            return stream
    return dub


def on_timeline(env, start):
    """ffmpeg decodes from a stream's first sample, and tracks start tens of ms apart."""
    shift = round(start * FPS)
    return np.concatenate([np.zeros(shift), env]) if shift > 0 else env[-shift:]


def normalise(env):
    trend = np.convolve(env, np.ones(int(FPS)) / FPS, mode="same")
    env = env - trend
    return (env - env.mean()) / (env.std() or 1.0)


def envelope(path, stream):
    """Loudness at FPS frames per second on the file's timeline, slow trend removed so effects stand out."""
    cmd = ["ffmpeg", "-v", "error", "-nostdin", "-i", str(path), "-map", f"0:{stream['index']}",
           "-ac", "1", "-ar", str(RATE), "-f", "s16le", "-"]
    frames, rest = [], np.empty(0, dtype=np.int16)
    with subprocess.Popen(cmd, stdout=subprocess.PIPE) as proc:
        while chunk := proc.stdout.read(HOP * 2 * 8192):
            samples = np.concatenate([rest, np.frombuffer(chunk, dtype=np.int16)])
            usable = len(samples) // HOP * HOP
            block = samples[:usable].astype(np.float32).reshape(-1, HOP)
            frames.append(np.sqrt((block ** 2).mean(axis=1)))
            rest = samples[usable:]
    if proc.returncode or not frames:
        raise SystemExit(f"{path.name}: ffmpeg could not decode stream #{stream['index']}")
    return on_timeline(normalise(np.log1p(np.concatenate(frames))), start_time(stream))


def stretch(env, speed):
    """The source envelope on the target's timeline, for t_source = speed * t_target."""
    n = int(len(env) / speed)
    return np.interp(np.arange(n) * speed, np.arange(len(env)), env, right=0.0)


def peak(corr):
    """Index of the maximum refined by a parabola, and how far it stands above the noise."""
    i = int(np.argmax(corr))
    shift = 0.0
    if 0 < i < len(corr) - 1:
        left, mid, right = corr[i - 1], corr[i], corr[i + 1]
        denom = left - 2 * mid + right
        shift = 0.5 * (left - right) / denom if denom else 0.0
    quality = (corr[i] - corr.mean()) / (corr.std() or 1.0)
    return i + shift, quality


def global_lag(ref, src):
    """Lag L in frames with src(t + L) ~ ref(t), searched within MAX_OFFSET_SECONDS."""
    size = 1 << int(np.ceil(np.log2(len(ref) + len(src))))
    corr = np.fft.irfft(np.conj(np.fft.rfft(ref, size)) * np.fft.rfft(src, size), size)
    reach = min(int(MAX_OFFSET_SECONDS * FPS), size // 2 - 1)
    i, quality = peak(np.concatenate([corr[-reach:], corr[:reach + 1]]))
    return i - reach, quality


def local_lags(ref, src, lag):
    """(time, lag, quality) for WINDOWS windows spread over the film, each in seconds."""
    width = min(int(WINDOW_SECONDS * FPS), len(ref) // 4)
    search = int(LOCAL_SEARCH_SECONDS * FPS)
    margin = int(len(ref) * 0.03)
    found = []
    for start in np.linspace(margin, len(ref) - margin - width, WINDOWS).astype(int):
        lo = int(round(start + lag)) - search
        hi = lo + width + 2 * search
        if lo < 0 or hi > len(src):
            continue
        i, quality = peak(np.correlate(src[lo:hi], ref[start:start + width], mode="valid"))
        found.append(((start + width / 2) / FPS, (lo + i - start) / FPS, quality))
    return found


def fit(windows):
    """lag(t) = c0 + c1 * t through the clear windows, or the reason there is no such line.

    Only one clear window may disagree, as a stray peak: a cut puts every window after it on another
    line, and discarding those as noise would mux a track that drifts out from the cut on."""
    good = [(t, lag) for t, lag, q in windows if q >= MIN_PEAK_QUALITY]
    if len(good) < MIN_GOOD_WINDOWS * WINDOWS:
        return None, f"only {len(good)} of {WINDOWS} windows matched clearly"
    points = np.array(good)
    c1, c0 = np.polyfit(points[:, 0], points[:, 1], 1)
    residual = np.abs(points[:, 1] - (c0 + c1 * points[:, 0]))
    if residual.max() > MAX_RESIDUAL_SECONDS:
        points = np.delete(points, int(np.argmax(residual)), axis=0)
        c1, c0 = np.polyfit(points[:, 0], points[:, 1], 1)
        residual = np.abs(points[:, 1] - (c0 + c1 * points[:, 0]))
    if residual.max() > MAX_RESIDUAL_SECONDS:
        return None, "the windows disagree: another cut or missing scenes"
    return (c0, c1, float(residual.max()), len(points)), None


def align(ref, raw):
    """The mapping t_raw = a * t_ref + b between two envelopes, or the reason there is none."""
    best = None
    for speed in SPEED_CANDIDATES:
        lag, quality = global_lag(ref, stretch(raw, speed))
        if best is None or quality > best[2]:
            best = (speed, lag, quality)
    speed, lag, quality = best
    windows = local_lags(ref, stretch(raw, speed), lag)
    line, failure = fit(windows)
    if failure:
        return None, failure, windows
    c0, c1, worst, used = line
    return {"a": speed * (1 + c1), "b": speed * c0, "global_quality": round(quality, 1),
            "worst_residual_ms": round(worst * 1000, 1), "windows_used": used}, None, windows


def plan_track(dub, mapping):
    """How the dub reaches the target's timeline: (copy?, b on the dub's own decoded timeline)."""
    b = mapping["b"] - start_time(dub)
    copy = abs(mapping["a"] - 1) < SAME_SPEED and dub.get("codec_name") == TARGET_CODEC
    return copy, b


def build_track(source, dub, mapping, workdir):
    """The dub as AC3 on the target's timeline, plus the delay mkvmerge must still apply in ms."""
    copy, b = plan_track(dub, mapping)
    out = Path(workdir) / "dub.mka"
    head = ["ffmpeg", "-v", "error", "-nostdin", "-i", str(source), "-map", f"0:{dub['index']}"]
    if copy:
        subprocess.run(head + ["-c", "copy", "-avoid_negative_ts", "make_zero", str(out)], check=True)
        return out, round(-b * 1000), "copied"
    a = mapping["a"]
    rate = int(dub.get("sample_rate") or 48000)
    offset = b / a
    filters = ["asetpts=PTS-STARTPTS"]
    if abs(a - 1) >= SAME_SPEED:
        filters += [f"asetrate={rate * a:.4f}", f"aresample={rate}"]
    if offset > 0:
        filters += [f"atrim=start={offset:.4f}", "asetpts=PTS-STARTPTS"]
    elif offset < 0:
        filters.append(f"adelay={-offset * 1000:.1f}:all=1")
    channels = min(int(dub.get("channels") or 6), compat.AC3_MAX_CHANNELS)
    subprocess.run(head + ["-af", ",".join(filters), "-c:a", TARGET_CODEC, "-b:a", compat.AC3_BITRATE,
                           "-ac", str(channels), str(out)], check=True, timeout=compat.FFMPEG_TIMEOUT)
    return out, 0, f"converted {dub.get('codec_name')} to AC3"


def mux(target, track, sync_ms, lang, make_default, out):
    cmd = ["mkvmerge", "-q", "-o", str(out), str(target),
           "--language", f"0:{LANGUAGE_TAGS.get(lang, lang)}", "--track-name", f"0:{TRACK_NAMES.get(lang, lang)}",
           "--default-track-flag", f"0:{'yes' if make_default else 'no'}"]
    if sync_ms:
        cmd += ["--sync", f"0:{sync_ms}"]
    result = subprocess.run(cmd + [str(track)], capture_output=True, text=True)
    if result.returncode >= 2:
        raise SystemExit(f"mkvmerge failed: {result.stdout.strip() or result.stderr.strip()}")


def check_added(added_env, dub_env, mapping, duration):
    """None when the added track is the dub moved by the measured mapping, else why not."""
    a, b = mapping["a"], mapping["b"]
    expected = b / a
    line, failure = fit(local_lags(added_env, stretch(dub_env, a), expected * FPS))
    if failure:
        return f"the added track does not line up: {failure}"
    c0, c1, _, _ = line
    drift = abs(c0 - expected) + abs(c1) * duration
    if drift > MAX_RESIDUAL_SECONDS:
        return f"the added track is off by up to {drift * 1000:.0f} ms"
    return None


def verify(target_info, out, source, dub, mapping):
    info = compat.probe(out)
    if not info:
        return "the new file cannot be read"
    before = float(target_info["format"]["duration"])
    after = float(info["format"]["duration"])
    if abs(before - after) > compat.DURATION_TOLERANCE:
        return f"duration changed from {before:.1f}s to {after:.1f}s"
    if len(info["streams"]) != len(target_info["streams"]) + 1:
        return "the stream count is not the original plus one"
    added = compat.audio_streams(info)[-1]
    if added.get("codec_name") != TARGET_CODEC:
        return f"the added track is {added.get('codec_name')}, not {TARGET_CODEC}"
    return check_added(envelope(out, added), envelope(source, dub), mapping, after)


def scoped():
    """Re-run inside a capped systemd scope when one can be made; nice and ionice either way."""
    if os.environ.get(SCOPED_ENV):
        return
    os.environ[SCOPED_ENV] = "1"
    os.environ.setdefault("XDG_RUNTIME_DIR", f"/run/user/{os.getuid()}")
    lowest = ["nice", "-n", "19"] + (["ionice", "-c3"] if shutil.which("ionice") else [])
    command = lowest + [sys.executable, os.path.abspath(__file__), *sys.argv[1:]]
    if shutil.which("systemd-run") and subprocess.run(SCOPE + ["true"], capture_output=True).returncode == 0:
        os.execvp("systemd-run", SCOPE + command)
    print("no systemd user scope here: running with nice and ionice only", file=sys.stderr)
    os.execvp(command[0], command)


def main():
    parser = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    parser.add_argument("target", type=Path)
    parser.add_argument("source", type=Path)
    parser.add_argument("--lang", default="spa")
    parser.add_argument("--track", type=int, help="ffprobe stream index of the dub in SOURCE")
    parser.add_argument("--default", action="store_true", help="make the added track the default one")
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    scoped()

    target, source = args.target.resolve(), args.source.resolve()
    if target.suffix != ".mkv":
        raise SystemExit(f"{target.name}: only .mkv targets, so the library path never changes")
    target_info, source_info = compat.probe(target), compat.probe(source)
    if not target_info or not source_info:
        raise SystemExit("ffprobe cannot read one of the files")
    aliases = LANGUAGE_ALIASES.get(args.lang, {args.lang})
    if any(language(s) in aliases for s in compat.audio_streams(target_info)):
        raise SystemExit(f"{target.name} already has a {args.lang} track")
    dub = pick_dub(source_info, args.lang, args.track)
    if not dub:
        raise SystemExit(f"{source.name}: no {args.lang} track; name one with --track")
    target_ref = main_track(target_info)
    source_ref = pick_reference(source_info, dub, target_ref)
    print(f"dub        {describe(dub)}")
    print(f"aligned on {describe(target_ref)} (target) against {describe(source_ref)} (source)")

    started = time.monotonic()
    mapping, failure, windows = align(envelope(target, target_ref), envelope(source, source_ref))
    for t, lag, q in windows:
        print(f"  window {t / 60:6.1f} min  lag {lag:+8.3f}s  quality {q:5.1f}")
    if failure:
        raise SystemExit(f"refused: {failure}")
    copy, _ = plan_track(dub, mapping)
    print(f"mapping    t_source = {mapping['a']:.6f} * t + {mapping['b']:+.3f}s  "
          f"(worst window {mapping['worst_residual_ms']} ms, {mapping['windows_used']}/{WINDOWS} windows, "
          f"measured in {time.monotonic() - started:.0f}s); the dub will be "
          f"{'copied' if copy else 'converted to AC3'}")
    if args.dry_run:
        return

    if shutil.disk_usage(target.parent).free < compat.MIN_FREE_BYTES:
        raise SystemExit("not enough free space")
    with compat.convert_lock("add-audio", wait=True) as holding:
        if holding is None:
            raise SystemExit(f"busy: {compat.lock_holder()}")
        holding(target)
        tmp = target.with_name(".add-audio.in-progress.mkv")
        tmp.unlink(missing_ok=True)
        try:
            with tempfile.TemporaryDirectory(dir=target.parent, prefix=".add-audio.") as workdir:
                track, sync_ms, how = build_track(source, dub, mapping, workdir)
                mux(target, track, sync_ms, args.lang, args.default, tmp)
            failure = verify(target_info, tmp, source, dub, mapping)
        except BaseException:
            tmp.unlink(missing_ok=True)
            raise
        outcome = {"result": "failed" if failure else "done", "target": "add-audio", "language": args.lang,
                   "source": source.name, "how": how, **{k: mapping[k] for k in ("a", "b", "worst_residual_ms")}}
        if failure:
            tmp.unlink(missing_ok=True)
            compat.record("add-audio", target, {**outcome, "reason": failure})
            raise SystemExit(f"not added: {failure}")
        target.rename(compat.free_original_name(target))
        tmp.rename(target)
        compat.record("add-audio", target, outcome)
    print(f"{target.name}: added {TRACK_NAMES.get(args.lang, args.lang)} ({how}), .original kept")


if __name__ == "__main__":
    sys.exit(main())
