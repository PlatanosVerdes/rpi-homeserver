#!/usr/bin/env python3
"""Tests for add-audio.py: the sync maths on synthetic envelopes, and whole runs on short generated films.

    python3 -m unittest scripts/sync/test_add_audio.py

The end-to-end cases need ffmpeg and mkvmerge and are skipped without them. ADD_AUDIO_TEST_DIR picks
where their files go (about 300 MB at once); on the Pi that belongs on the data disk, not in /tmp.
"""

import importlib.util
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
import wave
from pathlib import Path

import numpy as np

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("add_audio", HERE / "add-audio.py")
aa = importlib.util.module_from_spec(spec)
spec.loader.exec_module(aa)

PAL = 23.976 / 25
RNG = np.random.default_rng(7)


def film_envelope(seconds):
    raw = np.convolve(RNG.standard_normal(int(seconds * aa.FPS)), np.ones(20) / 20, mode="same")
    return raw


def as_source(env, a, b):
    """The same content on a source timeline where t_source = a * t_target + b."""
    n = int((len(env) / aa.FPS * a + b) * aa.FPS)
    target_time = (np.arange(n) / aa.FPS - b) / a
    return np.interp(target_time * aa.FPS, np.arange(len(env)), env, left=0.0, right=0.0)


def with_dialogue(env, level=0.8):
    return env + level * film_envelope(len(env) / aa.FPS)[:len(env)]


class SyncMaths(unittest.TestCase):
    def test_recovers_a_pal_speed_up_and_an_offset(self):
        content = film_envelope(40 * 60)
        mapping, failure, _ = aa.align(aa.normalise(with_dialogue(content)),
                                       aa.normalise(with_dialogue(as_source(content, PAL, 3.2))))
        self.assertIsNone(failure)
        self.assertAlmostEqual(mapping["a"], PAL, delta=1e-5)
        self.assertAlmostEqual(mapping["b"], 3.2, delta=0.01)

    def test_recovers_a_negative_offset_at_the_same_speed(self):
        content = film_envelope(40 * 60)
        mapping, failure, _ = aa.align(aa.normalise(content), aa.normalise(as_source(content, 1.0, -1.75)))
        self.assertIsNone(failure)
        self.assertAlmostEqual(mapping["a"], 1.0, delta=1e-5)
        self.assertAlmostEqual(mapping["b"], -1.75, delta=0.01)

    def test_refuses_a_source_with_a_scene_cut(self):
        content = film_envelope(40 * 60)
        source = as_source(content, 1.0, 0.5)
        cut = int(20 * 60 * aa.FPS)
        source = np.concatenate([source[:cut], source[cut + int(4 * aa.FPS):]])
        mapping, failure, _ = aa.align(aa.normalise(content), aa.normalise(source))
        self.assertIsNone(mapping)
        self.assertIn("disagree", failure)

    def test_refuses_a_different_film(self):
        mapping, failure, _ = aa.align(aa.normalise(film_envelope(40 * 60)), aa.normalise(film_envelope(40 * 60)))
        self.assertIsNone(mapping)
        self.assertIsNotNone(failure)

    def test_puts_the_start_time_back(self):
        env = np.arange(10.0)
        self.assertEqual(len(aa.on_timeline(env, 0.02)), 10 + 4)
        self.assertEqual(aa.on_timeline(env, 0.02)[4], 0.0)
        self.assertEqual(aa.on_timeline(env, -0.01)[0], 2.0)

    def test_check_added_accepts_the_mapped_dub_and_rejects_one_80_ms_off(self):
        dub = aa.normalise(film_envelope(30 * 60))
        mapping = {"a": 1.0, "b": 0.4}
        placed = aa.normalise(as_source(dub, 1.0, -0.4))
        self.assertIsNone(aa.check_added(placed, dub, mapping, 30 * 60))
        late = aa.normalise(as_source(dub, 1.0, -0.48))
        self.assertIn("off by", aa.check_added(late, dub, mapping, 30 * 60))


def stream(index, codec, lang, title="", channels=6, start=0.0):
    return {"index": index, "codec_type": "audio", "codec_name": codec, "channels": channels,
            "start_time": str(start), "tags": {"language": lang, "title": title}}


class Choices(unittest.TestCase):
    def test_prefers_castilian_ac3_and_skips_latin_and_commentary(self):
        info = {"streams": [stream(1, "dts", "spa", "Castellano DTS"), stream(2, "ac3", "spa", "Latino"),
                            stream(3, "ac3", "spa", "Castellano"), stream(4, "ac3", "spa", "Comentario del director"),
                            stream(5, "ac3", "eng")]}
        self.assertEqual(aa.pick_dub(info, "spa")["index"], 3)

    def test_takes_castilian_dts_over_latin_ac3(self):
        info = {"streams": [stream(1, "ac3", "spa", "Latino"), stream(2, "dts", "spa", "Castellano")]}
        self.assertEqual(aa.pick_dub(info, "spa")["index"], 2)

    def test_finds_nothing_when_only_latin_is_there(self):
        info = {"streams": [stream(1, "ac3", "spa", "Español Latino"), stream(2, "ac3", "spa", "LAT")]}
        self.assertIsNone(aa.pick_dub(info, "spa"))

    def test_copies_only_ac3_at_the_same_speed(self):
        self.assertEqual(aa.plan_track(stream(1, "ac3", "spa", start=0.02), {"a": 1.0, "b": 0.05}), (True, 0.05 - 0.02))
        self.assertFalse(aa.plan_track(stream(1, "dts", "spa"), {"a": 1.0, "b": 0.0})[0])
        self.assertFalse(aa.plan_track(stream(1, "eac3", "spa"), {"a": 1.0, "b": 0.0})[0])
        self.assertFalse(aa.plan_track(stream(1, "ac3", "spa"), {"a": PAL, "b": 0.0})[0])

    def test_aligns_on_the_source_track_in_the_target_language(self):
        info = {"streams": [stream(1, "ac3", "spa", "Castellano"), stream(2, "ac3", "eng", "Commentary"),
                            stream(3, "ac3", "eng")]}
        dub = aa.pick_dub(info, "spa")
        self.assertEqual(aa.pick_reference(info, dub, stream(1, "ac3", "eng"))["index"], 3)


RATE = 16000
SECONDS = 8 * 60


def effects(seconds, seed):
    """Noise in bursts of random length and loudness: what music and effects look like to the envelope."""
    rng = np.random.default_rng(seed)
    gains = np.repeat(rng.uniform(0, 1, int(seconds * 4)) ** 2, RATE // 4)
    return rng.standard_normal(len(gains)) * gains


def write_wav(path, samples):
    pcm = np.clip(samples / (np.abs(samples).max() or 1) * 0.8 * 32767, -32768, 32767).astype(np.int16)
    with wave.open(str(path), "wb") as out:
        out.setnchannels(1)
        out.setsampwidth(2)
        out.setframerate(RATE)
        out.writeframes(pcm.tobytes())


def on_source_clock(signal, a, b, start):
    """signal (target time) as a source track that begins at container time `start`."""
    n = int((len(signal) / RATE * a + b - start) * RATE)
    target_time = ((np.arange(n) / RATE + start) - b) / a
    return np.interp(target_time * RATE, np.arange(len(signal)), signal, left=0.0, right=0.0)


def ffmpeg(*args):
    subprocess.run(["ffmpeg", "-v", "error", "-y", "-nostdin", *args], check=True)


@unittest.skipUnless(shutil.which("ffmpeg") and shutil.which("mkvmerge"), "needs ffmpeg and mkvmerge")
class WholeRuns(unittest.TestCase):
    def setUp(self):
        self.dir = Path(tempfile.mkdtemp(prefix="add-audio-test.", dir=os.environ.get("ADD_AUDIO_TEST_DIR")))
        os.environ[aa.SCOPED_ENV] = "1"
        aa.compat.MIN_FREE_BYTES = 0
        aa.compat.STATE_DIR = self.dir
        aa.compat.LOCK = self.dir / "convert.lock"
        aa.compat.HISTORY = self.dir / "history.jsonl"
        shared = effects(SECONDS, 1)
        self.english = shared + 0.7 * effects(SECONDS, 2)
        self.spanish = shared + 0.7 * effects(SECONDS, 3)
        write_wav(self.dir / "eng.wav", self.english)
        self.target = self.dir / "Film (2000).mkv"
        ffmpeg("-f", "lavfi", "-i", "color=c=black:s=64x36:r=24000/1001", "-i", self.dir / "eng.wav",
               "-t", str(SECONDS), "-map", "0:v", "-map", "1:a", "-c:v", "libx264", "-preset", "ultrafast",
               "-c:a", "ac3", "-ar", "48000", "-metadata:s:a:0", "language=eng", self.target)

    def tearDown(self):
        shutil.rmtree(self.dir, ignore_errors=True)

    def make_source(self, a, b, spanish_codec, spanish_start, english_start, cut_at=None, tail=0):
        spanish = np.concatenate([on_source_clock(self.spanish, a, b, spanish_start), effects(tail, 4)])
        english = np.concatenate([on_source_clock(self.english, a, b, english_start), effects(tail, 5)])
        if cut_at is not None:
            cut = slice(int(cut_at * RATE), int((cut_at + 5) * RATE))
            spanish, english = np.delete(spanish, cut), np.delete(english, cut)
        write_wav(self.dir / "spa-src.wav", spanish)
        write_wav(self.dir / "eng-src.wav", english)
        source = self.dir / "source.mkv"
        codec = ["-c:a:0", "dca", "-strict", "-2"] if spanish_codec == "dts" else ["-c:a:0", "ac3"]
        ffmpeg("-f", "lavfi", "-i", "color=c=black:s=64x36:r=25", "-itsoffset", str(spanish_start),
               "-i", self.dir / "spa-src.wav", "-itsoffset", str(english_start), "-i", self.dir / "eng-src.wav",
               "-t", str(SECONDS * a + b + tail), "-map", "0:v", "-map", "1:a", "-map", "2:a", "-c:v", "libx264",
               "-preset", "ultrafast", *codec, "-c:a:1", "ac3", "-ar", "48000",
               "-metadata:s:a:0", "language=spa", "-metadata:s:a:0", "title=Castellano",
               "-metadata:s:a:1", "language=eng", source)
        return source

    def run_script(self, source):
        argv = sys.argv
        sys.argv = ["add-audio.py", str(self.target), str(source)]
        try:
            aa.main()
        finally:
            sys.argv = argv

    def added_lag(self):
        info = aa.compat.probe(self.target)
        tracks = aa.compat.audio_streams(info)
        added = tracks[-1]
        self.assertEqual(added.get("codec_name"), "ac3")
        self.assertEqual(aa.language(added), "spa")
        truth = self.dir / "spa-truth.wav"
        write_wav(truth, self.spanish)
        made = aa.envelope(self.target, added)
        expected = aa.envelope(truth, {"index": 0, "start_time": "0"})
        lag, _ = aa.global_lag(expected, made)
        return lag / aa.FPS

    def test_pal_dts_source_with_staggered_starts_is_converted_and_in_sync(self):
        source = self.make_source(PAL, 2.5, "dts", spanish_start=0.02, english_start=0.08)
        self.run_script(source)
        self.assertLess(abs(self.added_lag()), 0.03)
        self.assertTrue(self.target.with_name(self.target.name + ".original").exists())

    def test_same_speed_ac3_source_is_copied_and_in_sync(self):
        source = self.make_source(1.0, -0.6, "ac3", spanish_start=0.02, english_start=0.08)
        self.run_script(source)
        self.assertLess(abs(self.added_lag()), 0.03)

    def test_a_source_running_longer_is_cut_at_the_target_length(self):
        for codec, a in (("ac3", 1.0), ("dts", PAL)):
            with self.subTest(codec=codec):
                self.setUp()
                source = self.make_source(a, 1.5, codec, spanish_start=0.0, english_start=0.0, tail=90)
                self.run_script(source)
                self.assertLess(abs(self.added_lag()), 0.03)
                info = aa.compat.probe(self.target)
                self.assertAlmostEqual(float(info["format"]["duration"]), SECONDS, delta=aa.compat.DURATION_TOLERANCE)

    def test_a_sample_is_refused_before_aligning(self):
        source = self.make_source(1.0, 0.0, "ac3", spanish_start=0.0, english_start=0.0)
        sample = self.dir / "sample.mkv"
        ffmpeg("-i", source, "-t", "60", "-map", "0", "-c", "copy", sample)
        with self.assertRaisesRegex(SystemExit, "a sample or another cut"):
            self.run_script(sample)

    def test_a_cut_source_leaves_the_target_untouched(self):
        before = self.target.stat()
        source = self.make_source(1.0, 0.3, "ac3", spanish_start=0.0, english_start=0.0, cut_at=4 * 60)
        with self.assertRaises(SystemExit):
            self.run_script(source)
        self.assertEqual(self.target.stat().st_ino, before.st_ino)
        self.assertFalse(self.target.with_name(self.target.name + ".original").exists())
        self.assertEqual([p.name for p in self.dir.glob(".add-audio*")], [])


if __name__ == "__main__":
    unittest.main()
