#!/usr/bin/env python3
"""Tests for dub-fetch.py's choices: which films, which release, the torrent's hash.

    python3 -m unittest scripts/sync/test_dub_fetch.py
"""

import hashlib
import importlib.util
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("dub_fetch", HERE / "dub-fetch.py")
df = importlib.util.module_from_spec(spec)
spec.loader.exec_module(df)

GB = df.GB


def movie(movie_id, languages, path="/data/films/A (2000)/a.mkv", imdb="tt0000001", added="2026-01-01"):
    return {"id": movie_id, "title": f"Film {movie_id}", "year": 2000, "imdbId": imdb, "hasFile": True,
            "movieFile": {"path": path, "dateAdded": added, "mediaInfo": {"audioLanguages": languages}}}


def release(guid, size_gb, seeders=5, imdb=1, title="Film (2000) Spanish", flags=None):
    return {"guid": guid, "size": int(size_gb * GB), "seeders": seeders, "imdbId": imdb, "title": title,
            "indexerFlags": flags or []}


class Films(unittest.TestCase):
    def test_keeps_only_mkv_films_without_spanish_newest_first(self):
        films = df.films_without_language([
            movie(1, "eng", added="2026-01-01"), movie(2, "eng/spa"), movie(3, "Spanish / English"),
            movie(4, "eng", path="/data/films/B/b.mp4"), movie(5, "", added="2026-03-01"),
            {**movie(6, "eng"), "hasFile": False}])
        self.assertEqual([f["id"] for f in films], [5, 1])

    def test_maps_container_paths_to_the_host(self):
        self.assertEqual(df.host_path("/data/films/A/a.mkv"), df.DATA_ROOT / "films/A/a.mkv")
        self.assertEqual(df.host_path("/database/x"), Path("/database/x"))


class Releases(unittest.TestCase):
    def test_takes_the_smallest_live_release_of_this_film(self):
        chosen = df.choose_release([release("a", 8), release("b", 3), release("c", 2, seeders=0),
                                    release("d", 1, imdb=2), release("e", 2.5, title="Film LATINO")],
                                   "tt0000001", tried=set())
        self.assertEqual(chosen["guid"], "b")

    def test_prefers_freeleech_and_skips_what_was_tried_or_too_big(self):
        results = [release("a", 2), release("b", 6, flags=["freeleech"]), release("c", 40)]
        self.assertEqual(df.choose_release(results, "tt0000001", set())["guid"], "b")
        self.assertEqual(df.choose_release(results, "tt0000001", {"b"})["guid"], "a")
        self.assertIsNone(df.choose_release(results, "tt0000001", {"a", "b"}))

    def test_freeleech_costs_nothing_against_the_budget(self):
        self.assertEqual(df.charged_bytes(release("a", 2, flags=["freeleech"])), 0)
        self.assertEqual(df.charged_bytes(release("a", 2)), 2 * GB)


class Torrent(unittest.TestCase):
    def test_infohash_is_the_sha1_of_the_info_dict(self):
        info = b"d6:lengthi5e4:name5:a.mkv12:piece lengthi16384e6:pieces20:" + b"x" * 20 + b"e"
        torrent = b"d8:announce10:http://a/b4:info" + info + b"e"
        self.assertEqual(df.infohash(torrent), hashlib.sha1(info).hexdigest())

    def test_refuses_what_is_not_a_torrent(self):
        with self.assertRaises(ValueError):
            df.infohash(b"<html>login</html>")


if __name__ == "__main__":
    unittest.main()
