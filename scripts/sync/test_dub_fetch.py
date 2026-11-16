#!/usr/bin/env python3
"""Tests for dub-fetch.py: which films, which releases may be probed, requests, the torrent's hash.

    python3 -m unittest scripts/sync/test_dub_fetch.py
"""

import hashlib
import importlib.util
import json
import tempfile
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("dub_fetch", HERE / "dub-fetch.py")
df = importlib.util.module_from_spec(spec)
spec.loader.exec_module(df)

GB, MB = df.GB, df.MB
RULES = {"c411": {"prowlarr_indexer": "C411", "hit_and_run": {}}, "nobs": {"prowlarr_indexer": "NOBS", "hit_and_run": {"starts_at_progress": 0.5}},
         "tl": {"prowlarr_indexer": "TorrentLeech", "hit_and_run": {"min_seed_hours": 240}}}
NAMES = {1: "Public", 2: "NOBS", 3: "TorrentLeech", 4: "Unknown private", 5: "C411"}
INDEXERS = {1: {"private": False, "language": "en-US"}, 2: {"private": True, "language": "es-ES"},
            3: {"private": True, "language": "en-US"}, 4: {"private": True, "language": "en-US"},
            5: {"private": True, "language": "fr-FR"}}


def movie(movie_id, languages, path="/data/films/A (2000)/a.mkv"):
    return {"id": movie_id, "title": f"Film {movie_id}", "year": 2000, "imdbId": "tt0000001", "hasFile": True,
            "movieFile": {"path": path, "mediaInfo": {"audioLanguages": languages}}}


def release(guid, size_gb, indexer=1, seeders=5, imdb=1, title="Film (2000)", flags=None):
    return {"guid": guid, "size": int(size_gb * GB), "seeders": seeders, "imdbId": imdb, "title": title,
            "indexerId": indexer, "indexer": NAMES[indexer], "protocol": "torrent", "indexerFlags": flags or [],
            "downloadUrl": f"http://prowlarr:9696/{indexer}/download?apikey=secret&link=abc&file={guid}"}


def pick(results, tried=()):
    return [c["guid"] for c in df.candidates(results, "tt0000001", "spa", set(tried), INDEXERS, RULES)]


class Films(unittest.TestCase):
    def test_lists_mkv_films_with_the_languages_they_lack(self):
        listed = df.films([movie(1, "eng"), movie(2, "eng/spa"), movie(3, "Spanish / English"),
                           movie(4, "eng", path="/data/films/B/b.mp4"), {**movie(5, "eng"), "hasFile": False}])
        self.assertEqual([(f["id"], "spa" in f["missing"], "eng" in f["missing"]) for f in listed],
                         [(1, True, False), (2, False, False), (3, False, False)])

    def test_maps_container_paths_to_the_host(self):
        self.assertEqual(df.host_path("/data/films/A/a.mkv"), df.DATA_ROOT / "films/A/a.mkv")
        self.assertEqual(df.host_path("/database/x"), Path("/database/x"))


class Candidates(unittest.TestCase):
    def test_public_first_then_private_freeleech_then_private_smaller_first(self):
        results = [release("tl", 2, indexer=3), release("pub-big", 9), release("pub", 3),
                   release("tl-fl", 6, indexer=3, flags=["freeleech"])]
        self.assertEqual(pick(results), ["pub", "pub-big", "tl-fl", "tl"])

    def test_likely_carriers_of_the_language_go_first(self):
        results = [release("plain", 1), release("multi", 2, title="Film 2000 MULTi 1080p"),
                   release("french-multi", 0.5, indexer=5, title="Film 2000 MULTI VFF"),
                   release("spanish-site", 3, indexer=2), release("named", 4, indexer=3, title="Film 2000 Castellano")]
        self.assertEqual(pick(results), ["named", "spanish-site", "multi", "plain", "french-multi"])

    def test_keeps_a_release_without_an_imdb_id(self):
        self.assertEqual(pick([release("a", 2, imdb=0)]), ["a"])

    def test_skips_dead_tried_oversized_latin_or_other_films(self):
        results = [release("a", 2, seeders=0), release("b", 2), release("c", 40), release("d", 2, imdb=2),
                   release("e", 2, title="Film LATINO"), release("f", 1)]
        self.assertEqual(pick(results, tried={"f"}), ["b"])

    def test_probes_only_where_deleting_a_few_mb_owes_nothing(self):
        # TorrentLeech's obligation starts at completion; an unlisted private site is never touched.
        self.assertEqual(pick([release("tl", 2, indexer=3), release("x", 2, indexer=4)]), ["tl"])
        tiny = df.PROBE_BUDGET * df.PROBE_MARGIN * 2 / GB
        self.assertEqual(pick([release("nobs", tiny * 0.9, indexer=2)]), [])

    def test_never_keeps_prowlarrs_api_key(self):
        url = df.candidates([release("a", 2)], "tt0000001", "spa", set(), INDEXERS, RULES)[0]["url"]
        self.assertNotIn("secret", url)
        self.assertTrue(url.startswith("http://127.0.0.1:9696/1/download?link=abc"))

    def test_only_counted_download_costs_budget(self):
        candidate = {"size": 2 * GB, "private": True, "freeleech": False}
        self.assertEqual(df.charged_bytes(candidate), 2 * GB)
        self.assertEqual(df.charged_bytes({**candidate, "freeleech": True}), 0)
        self.assertEqual(df.charged_bytes({**candidate, "private": False}), 0)


class Probe(unittest.TestCase):
    def test_ready_bytes_counts_the_downloaded_run_from_the_files_start(self):
        self.assertEqual(df.ready_bytes([2, 2, 2, 0, 2], 0, 0, MB), 3 * MB)
        self.assertEqual(df.ready_bytes([2, 2, 2, 0], 1, MB // 2, MB), 2 * MB + MB // 2)
        self.assertEqual(df.ready_bytes([0, 2], 0, 0, MB), 0)


class Requests(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        df.REQUESTS_DIR = Path(self.dir.name)
        self.run = df.Pass({}, {"jobs": {}, "charged_bytes": 0})
        self.listed = {str(f["id"]): f for f in df.films([movie(1, "eng"), movie(2, "spa")])}

    def tearDown(self):
        self.dir.cleanup()

    def request(self, movie_id, **extra):
        (df.REQUESTS_DIR / f"{movie_id}-spa.json").write_text(json.dumps({"movie_id": movie_id, "lang": "spa", **extra}))

    def test_a_request_for_a_film_lacking_the_language_becomes_a_job(self):
        self.request(1)
        self.request(2)
        self.run.take_requests(self.listed)
        self.assertEqual(list(self.run.state["jobs"]), ["1-spa"])
        self.assertEqual(self.run.state["jobs"]["1-spa"]["status"], "queued")
        self.assertEqual(list(df.REQUESTS_DIR.iterdir()), [])

    def test_cancel_ends_a_job_that_has_no_torrent_yet(self):
        self.request(1)
        self.run.take_requests(self.listed)
        self.request(1, cancel=True)
        self.run.take_requests(self.listed)
        self.assertEqual(self.run.state["jobs"]["1-spa"]["status"], "cancelled")


class Checks(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        df.REQUESTS_DIR = Path(self.dir.name) / "requests"
        df.REQUESTS_DIR.mkdir()
        df.CHECKS_FILE = Path(self.dir.name) / "checks.json"
        self.run = df.Pass({}, {"jobs": {}, "charged_bytes": 0})
        self.listed = {str(f["id"]): f for f in df.films([movie(1, "eng")])}
        results = [release("named", 2, title="Film 2000 Castellano"), release("site", 3, indexer=2), release("plain", 1)]
        self.run.find = lambda imdb, name, lang, tried=(): df.candidates(results, imdb, lang, set(tried), INDEXERS, RULES)

    def tearDown(self):
        self.dir.cleanup()

    def test_a_check_reports_what_a_search_finds_and_starts_no_job(self):
        (df.REQUESTS_DIR / "1-spa-check.json").write_text(json.dumps({"movie_id": 1, "lang": "spa", "check": True}))
        self.run.take_requests(self.listed)
        self.assertEqual(self.run.state["jobs"], {})
        self.run.run_checks(self.listed)
        check = json.loads(df.CHECKS_FILE.read_text())["1-spa"]
        self.assertEqual((check["releases"], check["named"], check["own_site"]), (3, 1, 1))
        self.assertEqual(check["best"][0]["title"], "Film 2000 Castellano")
        self.assertNotIn("url", check["best"][0])
        self.assertEqual(list(df.REQUESTS_DIR.iterdir()), [])


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
