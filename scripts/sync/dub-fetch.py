#!/usr/bin/env python3
"""Fetch a dub for a library film when the audio-manager page asks for one, and add it with add-audio.py.

    scripts/sync/dub-fetch.py             # one pass: list the films, take new requests, advance the job
    scripts/sync/dub-fetch.py --status    # the jobs and the budget
    scripts/sync/dub-fetch.py --candidates RADARR_ID   # what it would probe for that film, touching nothing

Nothing is fetched unless asked. Each pass, from cron every minute:

1. Writes films.json, at most every ten minutes: every .mkv film in Radarr and the languages it lacks.
2. Answers availability checks: what a search finds for a film in a language, downloading nothing.
3. Turns the page's request files into jobs, one film at a time, oldest first.
4. Advances the job. Every indexer Prowlarr has is searched; a release is only tried where taking a
   few MB and deleting it owes nothing (public, or private with a hit & run rule in
   config/trackers/rules.json that starts well above that). Each candidate is probed before it is
   downloaded: the first pieces of its .mkv hold the track list, so a release without the language
   is deleted after a few MB instead of downloaded whole. One that has it is downloaded and handed
   to add-audio.py.

Seeding and deletion stay qbit-manage's: the category audio-source is never hardlinked, so it is
tagged noHL and its tracker's share-limit group applies. The `keep` tag holds a torrent out of those
groups only while this needs its file, which matters for a public one: that group deletes after 2 h.
See docs/add-audio.md.
"""

import argparse
import fcntl
import hashlib
import http.cookiejar
import importlib.util
import json
import os
import re
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from pathlib import Path

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("add_audio", HERE / "add-audio.py")
add_audio = importlib.util.module_from_spec(spec)
spec.loader.exec_module(add_audio)

PROJECT_DIR = Path(os.path.expanduser("~/rpi-homeserver"))
ENV_FILE = PROJECT_DIR / ".env"
RULES_FILE = PROJECT_DIR / "config" / "trackers" / "rules.json"
JOBS_DIR = add_audio.compat.STATE_DIR / "add-audio"
REQUESTS_DIR = JOBS_DIR / "requests"
FILMS_FILE = JOBS_DIR / "films.json"
STATE_FILE = JOBS_DIR / "state.json"
CHECKS_FILE = JOBS_DIR / "checks.json"
FILMS_EVERY = 600
DATA_ROOT = Path(os.environ.get("DATA_ROOT", "/mnt/data"))
CONTAINER_DATA = "/data"
RADARR = "http://127.0.0.1:7878"
PROWLARR = "http://127.0.0.1:9696"
CATEGORY = "audio-source"
KEEP_TAG = "keep"
SAVE_PATH = "/data/downloads"
MULTI_AUDIO = re.compile(r"\bmulti|\bdual\b", re.I)
GB = 1024**3
MB = 1024**2
BUDGET_GB = 100
MAX_SIZE_GB = 15
# A Matroska file lists its tracks in its first few hundred KB; a probe reads what is there from 2 MB on,
# and gives up on a release that has taken PROBE_BUDGET without showing them.
PROBE_HEAD = 2 * MB
PROBE_BUDGET = 64 * MB
# A probe stays this far under the progress at which a site's hit & run starts.
PROBE_MARGIN = 4
MAX_PROBES = 8
MAX_DOWNLOADS = 2
PROBE_HOURS = 6
# A probe downloads slowly and is watched until its pieces are in, then stopped: left running, a fast
# swarm took 300 MB in the ten seconds between two checks.
PROBE_RATE = 2 * MB
PROBE_WATCH_SECONDS = 300
PROBE_POLL_SECONDS = 3
STALLED_HOURS = 48
ADD_AUDIO_TIMEOUT = 4 * 3600
HOUR = 3600
DONE = ("done", "failed", "cancelled")


def env_values():
    values = {}
    try:
        for line in ENV_FILE.read_text().splitlines():
            if "=" in line and not line.lstrip().startswith("#"):
                key, _, value = line.strip().partition("=")
                values[key] = value.strip('"').strip("'")
    except OSError:
        pass
    return {**values, **{k: v for k, v in os.environ.items() if k in values}}


def http_json(url, headers=None, data=None, opener=None, timeout=120):
    request = urllib.request.Request(url, data=data, headers=headers or {})
    with (opener.open if opener else urllib.request.urlopen)(request, timeout=timeout) as response:
        body = response.read()
    return json.loads(body) if body.strip() else None


def write_json(path, payload):
    tmp = path.with_suffix(".tmp")
    tmp.write_text(json.dumps(payload, indent=1, sort_keys=True, ensure_ascii=False))
    tmp.replace(path)


def host_path(container_path):
    """Radarr and qBittorrent both mount DATA_ROOT at /data."""
    path = str(container_path)
    if path == CONTAINER_DATA or path.startswith(CONTAINER_DATA + "/"):
        return DATA_ROOT / path[len(CONTAINER_DATA) + 1:]
    return Path(path)


def language_words(lang):
    known = add_audio.LANGUAGES[lang]
    return known["aliases"] | known["words"]


def has_language(audio_languages, lang):
    words = {w.strip().lower() for w in re.split(r"[/,]", audio_languages or "")}
    return bool(words & language_words(lang))


def films(movies):
    """Every .mkv film Radarr holds and which of the languages this can add it lacks."""
    out = []
    for movie in movies:
        file = movie.get("movieFile") or {}
        if not movie.get("hasFile") or not movie.get("imdbId") or not file.get("path", "").endswith(".mkv"):
            continue
        audio = (file.get("mediaInfo") or {}).get("audioLanguages") or ""
        audio = "/".join(dict.fromkeys(w.strip() for w in audio.split("/") if w.strip()))
        out.append({"id": movie["id"], "title": movie["title"], "year": movie.get("year"), "imdb": movie["imdbId"],
                    "path": file["path"], "audio": audio,
                    "missing": [lang for lang in add_audio.TRACK_NAMES if not has_language(audio, lang)]})
    return sorted(out, key=lambda f: f["title"].lower())


def is_freeleech(release):
    return "freeleech" in json.dumps(release.get("indexerFlags") or []).lower()


def probe_allowed(release, private, rules):
    """Whether a few MB of this release can be taken and deleted without owing its site anything."""
    if not private:
        return True
    rule = next((r for r in rules.values() if r.get("prowlarr_indexer") == release.get("indexer")), None)
    if rule is None:
        return False
    starts_at = (rule.get("hit_and_run") or {}).get("starts_at_progress", 1)
    return PROBE_BUDGET * PROBE_MARGIN < starts_at * release["size"]


def candidates(results, imdb, lang, tried, indexers, rules):
    """Releases worth probing, those most likely to carry the language first, then public before
    private, freeleech before counted, smaller first. A release without an IMDb id is kept: some
    sites leave it out, and add-audio refuses another film anyway."""
    number = int(imdb.lstrip("t") or 0)
    known = add_audio.LANGUAGES[lang]
    # What makes a release worth probing early: its title names the language, or its site speaks it.
    # Words only: language codes are ordinary words in titles too ("De", "Cat People", "No").
    hint = re.compile(r"\b(" + "|".join(map(re.escape, sorted(known["words"]))) + r")\b", re.I)
    rejected = re.compile(known["rejected"], re.I) if known.get("rejected") else None
    fits = []
    for r in results:
        site = indexers.get(r.get("indexerId"))
        if (site is None or int(r.get("imdbId") or number) != number or (r.get("seeders") or 0) <= 0
                or not 0 < r.get("size", 0) <= MAX_SIZE_GB * GB or r.get("guid") in tried
                or r.get("protocol") != "torrent" or not r.get("downloadUrl")
                or (rejected and rejected.search(r.get("title", ""))) or not probe_allowed(r, site["private"], rules)):
            continue
        title = r["title"]
        locale = (site["language"] or "").split("-")[0]
        own_site = locale == known["locale"]
        # MULTi on a French site is French plus the original; it only hints here on a site of ours or English.
        multi = MULTI_AUDIO.search(title) and (own_site or locale == "en")
        likely = (not hint.search(title), not own_site, not multi)
        fits.append({"guid": r["guid"], "title": title, "size": r["size"], "indexer": r.get("indexer"),
                     "private": site["private"], "freeleech": is_freeleech(r), "url": without_key(r["downloadUrl"]),
                     "rank": [*likely, site["private"], not is_freeleech(r), r["size"]]})
    return sorted(fits, key=lambda c: c["rank"])


def charged_bytes(candidate):
    return candidate["size"] if candidate["private"] and not candidate["freeleech"] else 0


def _bencode_end(data, i):
    head = data[i:i + 1]
    if head == b"i":
        return data.index(b"e", i) + 1
    if head in (b"l", b"d"):
        i += 1
        while data[i:i + 1] != b"e":
            i = _bencode_end(data, i)
        return i + 1
    colon = data.index(b":", i)
    return colon + 1 + int(data[i:colon])


def infohash(torrent):
    """SHA-1 of the bencoded info dict, which is how qBittorrent names the torrent."""
    if torrent[:1] != b"d":
        raise ValueError("not a torrent file")
    i = 1
    while torrent[i:i + 1] != b"e":
        key_end = _bencode_end(torrent, i)
        key = torrent[torrent.index(b":", i) + 1:key_end]
        value_end = _bencode_end(torrent, key_end)
        if key == b"info":
            return hashlib.sha1(torrent[key_end:value_end]).hexdigest()
        i = value_end
    raise ValueError("torrent has no info dict")


def ready_bytes(states, first_piece, offset, piece_size):
    """How many bytes from the start of a file are downloaded, given where it starts in the torrent."""
    done = 0
    while first_piece + done < len(states) and states[first_piece + done] == 2:
        done += 1
    return max(0, (first_piece + done) * piece_size - offset)


def progress(job):
    return job["status"], (job.get("current") or {}).get("stage"), len(job["tried"])


def without_key(url):
    """Prowlarr's download link, minus the API key it carries, which goes in a header instead."""
    parts = urllib.parse.urlsplit(url)
    query = [(k, v) for k, v in urllib.parse.parse_qsl(parts.query) if k.lower() != "apikey"]
    return urllib.parse.urlunsplit(parts._replace(scheme="http", netloc="127.0.0.1:9696",
                                                  query=urllib.parse.urlencode(query)))


class Qbit:
    def __init__(self, env):
        self.base = f"http://127.0.0.1:{env.get('QBIT_WEBUI_PORT', '8080')}/api/v2"
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        login = urllib.parse.urlencode({"username": env.get("QBIT_USER", ""),
                                        "password": env.get("QBIT_PASSWORD", "")}).encode()
        with self.opener.open(urllib.request.Request(f"{self.base}/auth/login", data=login,
                                                     headers={"Referer": self.base}), timeout=30) as r:
            # 5.2 answers 204 with no body, older versions 200 "Ok."; a refusal is 200 "Fails.".
            if r.status != 204 and r.read().strip() != b"Ok.":
                raise SystemExit("qBittorrent refused the login")

    def get(self, path, **params):
        """None for a torrent qBittorrent does not know (yet: an add takes a moment to register)."""
        try:
            return http_json(f"{self.base}/{path}?{urllib.parse.urlencode(params)}", opener=self.opener)
        except urllib.error.HTTPError as err:
            if err.code == 404:
                return None
            raise

    def post(self, path, **fields):
        data = urllib.parse.urlencode(fields).encode()
        self.opener.open(urllib.request.Request(f"{self.base}/{path}", data=data), timeout=60).close()

    def torrent(self, torrent_hash):
        found = self.get("torrents/info", hashes=torrent_hash)
        return found[0] if found else None

    def ensure_category(self):
        if CATEGORY not in (self.get("torrents/categories") or {}):
            self.post("torrents/createCategory", category=CATEGORY, savePath=SAVE_PATH)

    def add_stopped(self, torrent, name):
        boundary = uuid.uuid4().hex
        parts = []
        # stopped is 5.x's name, paused 4.x's; each ignores the other.
        for key, value in (("category", CATEGORY), ("tags", f"{CATEGORY},{KEEP_TAG}"), ("savepath", SAVE_PATH),
                           ("autoTMM", "false"), ("stopped", "true"), ("paused", "true")):
            parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{key}"\r\n\r\n{value}\r\n'.encode())
        parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="torrents"; filename="{name}.torrent"\r\n'
                     f"Content-Type: application/x-bittorrent\r\n\r\n".encode() + torrent + b"\r\n")
        parts.append(f"--{boundary}--\r\n".encode())
        request = urllib.request.Request(f"{self.base}/torrents/add", data=b"".join(parts),
                                         headers={"Content-Type": f"multipart/form-data; boundary={boundary}"})
        with self.opener.open(request, timeout=60) as r:
            # Older versions answer "Ok.", 5.2 a JSON summary; only "Fails." is a refusal.
            if r.read().strip() == b"Fails.":
                raise RuntimeError("qBittorrent did not take the torrent")

    def start(self, torrent_hash):
        self._either("torrents/start", "torrents/resume", torrent_hash)

    def stop(self, torrent_hash):
        self._either("torrents/stop", "torrents/pause", torrent_hash)

    def _either(self, current, older, torrent_hash):
        """5.x renamed resume/pause to start/stop."""
        try:
            self.post(current, hashes=torrent_hash)
        except urllib.error.HTTPError as err:
            if err.code != 404:
                raise
            self.post(older, hashes=torrent_hash)

    def delete(self, torrent_hash):
        self.post("torrents/delete", hashes=torrent_hash, deleteFiles="true")

    def release(self, torrent_hash):
        """Hands the torrent to qbit-manage's share limits."""
        self.post("torrents/removeTags", hashes=torrent_hash, tags=KEEP_TAG)


class Pass:
    def __init__(self, env, state):
        self.env, self.state = env, state
        self.radarr = {"X-Api-Key": env.get("RADARR_API_KEY", "")}
        self.prowlarr = {"X-Api-Key": env.get("PROWLARR_API_KEY", "")}
        self.budget = float(env.get("DUB_FETCH_BUDGET_GB") or BUDGET_GB) * GB
        self.events = []
        self._qbit = None

    @property
    def qbit(self):
        if self._qbit is None:
            self._qbit = Qbit(self.env)
        return self._qbit

    def say(self, job, line):
        line = f"{job['title']}: {line}"
        print(line)
        self.events.append(line)

    def set(self, job, status, reason=""):
        job.update(status=status, reason=reason, updated=int(time.time()))

    def list_films(self):
        """Radarr's films, asked again every FILMS_EVERY seconds or when a request is waiting."""
        try:
            cached = json.loads(FILMS_FILE.read_text())
        except (OSError, ValueError):
            cached = None
        fresh = cached and cached.get("languages") == add_audio.TRACK_NAMES and time.time() - cached["time"] < FILMS_EVERY
        if fresh and not any(REQUESTS_DIR.glob("*.json")):
            listed = cached["films"]
        else:
            listed = films(http_json(f"{RADARR}/api/v3/movie", self.radarr) or [])
            write_json(FILMS_FILE, {"time": int(time.time()), "languages": add_audio.TRACK_NAMES, "films": listed})
        return {str(f["id"]): f for f in listed}

    def run_checks(self, listed):
        """Answers the page's "is there a release in this language" without downloading anything."""
        try:
            checks = json.loads(CHECKS_FILE.read_text())
        except (OSError, ValueError):
            checks = {}
        for request_file in sorted(REQUESTS_DIR.glob("*-check.json")):
            try:
                request = json.loads(request_file.read_text())
                key, film = f"{int(request['movie_id'])}-{request['lang']}", listed.get(str(request["movie_id"]))
            except (OSError, ValueError, KeyError):
                request_file.unlink(missing_ok=True)
                continue
            if film and request["lang"] in film["missing"]:
                try:
                    found = self.find(film["imdb"], film["title"], request["lang"])
                    checks[key] = {"time": int(time.time()), "releases": len(found),
                                   "named": sum(not c["rank"][0] for c in found),
                                   "own_site": sum(c["rank"][0] and not c["rank"][1] for c in found),
                                   "best": [{k: c[k] for k in ("title", "size", "indexer", "private")} for c in found[:3]]}
                except (OSError, ValueError) as err:
                    checks[key] = {"time": int(time.time()), "releases": 0, "named": 0, "own_site": 0, "best": [],
                                   "error": str(err)}
            request_file.unlink(missing_ok=True)
            write_json(CHECKS_FILE, checks)

    def take_requests(self, listed):
        for request_file in sorted(REQUESTS_DIR.glob("*.json")):
            if request_file.name.endswith("-check.json"):
                continue
            try:
                request = json.loads(request_file.read_text())
                key = f"{int(request['movie_id'])}-{request['lang']}"
            except (OSError, ValueError, KeyError):
                request_file.unlink(missing_ok=True)
                continue
            job = self.state["jobs"].get(key)
            if request.get("cancel"):
                if job and job["status"] not in DONE:
                    self.drop_torrent(job)
                    self.set(job, "cancelled", "cancelled from the page")
            elif job is None or job["status"] in DONE:
                film = listed.get(str(request["movie_id"]))
                if film and request["lang"] in film["missing"]:
                    self.state["jobs"][key] = {
                        "movie_id": film["id"], "lang": request["lang"], "title": f"{film['title']} ({film['year']})",
                        "name": film["title"],
                        "imdb": film["imdb"], "requested": int(time.time()), "tried": [], "downloads": 0,
                        "status": "queued", "reason": "", "updated": int(time.time())}
            request_file.unlink(missing_ok=True)

    def drop_torrent(self, job):
        """Deletes a probe; a full download goes to qbit-manage, which owes its site the seeding."""
        current = job.get("current")
        if not current:
            return
        if self.qbit.torrent(current["hash"]):
            if current["stage"] == "probing" and self.safe_to_delete(current):
                self.qbit.delete(current["hash"])
            else:
                self.qbit.release(current["hash"])
        job["current"] = None

    def safe_to_delete(self, current):
        torrent = self.qbit.torrent(current["hash"])
        return (torrent is None or not current["private"]
                or torrent["downloaded"] * PROBE_MARGIN < current["delete_below"])

    def advance(self, job):
        """One step at a time until the job waits on something outside this pass."""
        while job["status"] not in DONE:
            before = progress(job)
            if job["status"] == "queued":
                self.search(job)
            elif job["status"] == "trying":
                self.try_next(job)
            elif job["status"] == "probing":
                self.check_probe(job)
            elif job["status"] == "downloading":
                self.check_download(job)
            if progress(job) == before:
                return

    def find(self, imdb, name, lang, tried=()):
        """By IMDb id everywhere, and by title for sites whose IMDb search finds nothing (NOBS):
        a title match only counts with the same IMDb id."""
        indexers = {i["id"]: {"private": i.get("privacy") != "public", "language": i.get("language")}
                    for i in http_json(f"{PROWLARR}/api/v1/indexer", self.prowlarr) or [] if i.get("enable")}
        number = int(imdb.lstrip("t") or 0)
        results = {}
        for query, strict in ((f"{{ImdbId:{imdb}}}", False), (name, True)):
            # Named one by one: without indexerIds Prowlarr leaves some enabled indexers out of the search.
            params = urllib.parse.urlencode([("query", query), ("type", "search"), ("categories", 2000),
                                             *(("indexerIds", i) for i in indexers)])
            for r in http_json(f"{PROWLARR}/api/v1/search?{params}", self.prowlarr, timeout=300) or []:
                if not strict or int(r.get("imdbId") or 0) == number:
                    results.setdefault(r.get("guid"), r)
        return candidates(list(results.values()), imdb, lang, set(tried), indexers, json.loads(RULES_FILE.read_text()))

    def search(self, job):
        job["candidates"] = self.find(job["imdb"], job["name"], job["lang"], job["tried"])
        if not job["candidates"]:
            self.set(job, "failed", "no release to try on any indexer")
            self.say(job, "no release to try on any indexer")
            return
        self.set(job, "trying", f"{len(job['candidates'])} releases to probe")

    def try_next(self, job):
        if len(job["tried"]) >= MAX_PROBES or not job.get("candidates"):
            self.set(job, "failed", f"none of the {len(job['tried'])} releases probed carried it")
            self.say(job, job["reason"])
            return
        candidate = job["candidates"].pop(0)
        job["tried"].append(candidate["guid"])
        if self.state.get("charged_bytes", 0) + charged_bytes(candidate) > self.budget:
            self.set(job, "trying", f"{candidate['title']} would pass the download budget")
            return
        request = urllib.request.Request(candidate["url"], headers=self.prowlarr)
        with urllib.request.urlopen(request, timeout=60) as response:
            torrent = response.read()
        torrent_hash = infohash(torrent)
        if self.qbit.torrent(torrent_hash):
            self.set(job, "trying", f"{candidate['title']} is already in qBittorrent")
            return
        self.qbit.ensure_category()
        self.qbit.add_stopped(torrent, job["title"])
        files = []
        for _ in range(10):
            files = self.qbit.get("torrents/files", hash=torrent_hash) or []
            if files:
                break
            time.sleep(1)
        videos = [(i, f) for i, f in enumerate(files) if f["name"].lower().endswith(".mkv")]
        if not videos:
            self.qbit.delete(torrent_hash)
            self.set(job, "trying", f"{candidate['title']} has no .mkv")
            return
        index, video = max(videos, key=lambda v: v[1]["size"])
        others = [str(i) for i, _ in enumerate(files) if i != index]
        if others:
            self.qbit.post("torrents/filePrio", hash=torrent_hash, id="|".join(others), priority=0)
        self.qbit.post("torrents/toggleSequentialDownload", hashes=torrent_hash)
        self.qbit.post("torrents/setDownloadLimit", hashes=torrent_hash, limit=PROBE_RATE)
        starts_at = 1.0 if not candidate["private"] else self.starts_at(candidate)
        job["current"] = {**candidate, "hash": torrent_hash, "file": index, "stage": "probing",
                          "since": int(time.time()), "delete_below": starts_at * candidate["size"]}
        self.set(job, "probing", candidate["title"])

    def starts_at(self, candidate):
        rules = json.loads(RULES_FILE.read_text())
        rule = next((r for r in rules.values() if r.get("prowlarr_indexer") == candidate["indexer"]), {})
        return (rule.get("hit_and_run") or {}).get("starts_at_progress", 1)

    def check_probe(self, job):
        current = job["current"]
        torrent = self.qbit.torrent(current["hash"])
        if torrent is None:
            job["current"] = None
            self.set(job, "trying", "the probe left qBittorrent")
            return
        files = self.qbit.get("torrents/files", hash=current["hash"]) or []
        file = files[current["file"]]
        offset = sum(f["size"] for f in files[:current["file"]])
        piece_size = self.qbit.get("torrents/properties", hash=current["hash"])["piece_size"]
        base = torrent.get("download_path") or torrent["save_path"]
        path = host_path(base) / file["name"]
        budget = PROBE_BUDGET if not current["private"] else min(PROBE_BUDGET, current["delete_below"] / PROBE_MARGIN)
        self.qbit.start(current["hash"])
        deadline, info, read = time.time() + PROBE_WATCH_SECONDS, None, 0
        try:
            while time.time() < deadline:
                states = self.qbit.get("torrents/pieceStates", hash=current["hash"]) or []
                ready = min(ready_bytes(states, file["piece_range"][0], offset, piece_size), file["size"], PROBE_BUDGET)
                if ready >= min(PROBE_HEAD, file["size"]) and ready > read:
                    info, read = self.probe_head(path, ready), ready
                    if info and add_audio.compat.audio_streams(info):
                        break
                if (self.qbit.torrent(current["hash"]) or {}).get("downloaded", 0) >= budget:
                    break
                time.sleep(PROBE_POLL_SECONDS)
        finally:
            self.qbit.stop(current["hash"])
        downloaded = (self.qbit.torrent(current["hash"]) or {}).get("downloaded", 0)
        if not (info and add_audio.compat.audio_streams(info)):
            if downloaded >= budget:
                self.drop_torrent(job)
                self.set(job, "trying", f"{current['title']}: no track list in {downloaded // MB} MB")
            elif time.time() - current["since"] > PROBE_HOURS * HOUR:
                self.drop_torrent(job)
                self.set(job, "trying", f"{current['title']}: no data after {PROBE_HOURS} h")
            return
        dub = add_audio.pick_dub(info, job["lang"])
        if dub is None:
            found = ", ".join(add_audio.describe(s) for s in add_audio.compat.audio_streams(info))
            self.drop_torrent(job)
            self.set(job, "trying", f"{current['title']}: no {add_audio.TRACK_NAMES[job['lang']]} track ({found})")
            return
        self.qbit.post("torrents/toggleSequentialDownload", hashes=current["hash"])
        self.qbit.post("torrents/setDownloadLimit", hashes=current["hash"], limit=0)
        self.qbit.start(current["hash"])
        current.update(stage="downloading", since=int(time.time()), dub=add_audio.describe(dub))
        self.state["charged_bytes"] = self.state.get("charged_bytes", 0) + charged_bytes(current)
        job["downloads"] += 1
        self.set(job, "downloading", f"{current['title']}, {current['size'] / GB:.1f} GB")
        self.say(job, f"downloading {current['title']} ({current['size'] / GB:.1f} GB): {job['current']['dub']}")

    def probe_head(self, path, size):
        """ffprobe's view of the first bytes of a partial download, or None."""
        try:
            with open(path, "rb") as source, tempfile.NamedTemporaryFile(suffix=".mkv") as head:
                head.write(source.read(size))
                head.flush()
                return add_audio.compat.probe(head.name)
        except OSError as err:
            print(f"probe of {path} failed: {err}", file=sys.stderr)
            return None

    def check_download(self, job):
        current = job["current"]
        torrent = self.qbit.torrent(current["hash"])
        if torrent is None:
            job["current"] = None
            self.next_or_fail(job, "the download left qBittorrent")
            return
        if torrent["progress"] < 1:
            if time.time() - current["since"] > STALLED_HOURS * HOUR:
                self.drop_torrent(job)
                self.next_or_fail(job, f"{current['title']}: {torrent['progress']:.0%} after {STALLED_HOURS} h")
            return
        movie = http_json(f"{RADARR}/api/v3/movie/{job['movie_id']}", self.radarr)
        target = host_path((movie.get("movieFile") or {}).get("path", ""))
        files = self.qbit.get("torrents/files", hash=current["hash"]) or []
        source = host_path(torrent["save_path"]) / files[current["file"]]["name"]
        if not target.name.endswith(".mkv") or not target.exists():
            self.drop_torrent(job)
            self.set(job, "failed", "the library file is gone or no longer .mkv")
            self.say(job, job["reason"])
            return
        last, code = self.run_add_audio(target, source, job["lang"])
        if last.startswith("busy:"):
            print(f"{job['title']}: {last}, next pass")
            return
        self.drop_torrent(job)
        if code == 0 or "already has a" in last:
            self.set(job, "done", last)
            self.say(job, f"done: {last}")
        else:
            self.next_or_fail(job, f"add-audio refused it: {last}")

    def run_add_audio(self, target, source, lang):
        try:
            run = subprocess.run([sys.executable, str(HERE / "add-audio.py"), str(target), str(source), "--lang", lang],
                                 capture_output=True, text=True, timeout=ADD_AUDIO_TIMEOUT)
            output, code = (run.stdout + run.stderr).strip(), run.returncode
        except subprocess.TimeoutExpired:
            output, code = "add-audio timed out", 1
        print(output)
        return (output.splitlines()[-1] if output else f"exit {code}"), code

    def next_or_fail(self, job, reason):
        if job["downloads"] >= MAX_DOWNLOADS:
            self.set(job, "failed", reason)
            self.say(job, f"gave up: {reason}")
        else:
            self.set(job, "trying", reason)

    def notify(self):
        token, chat = self.env.get("TELEGRAM_ALERT_BOT_TOKEN"), self.env.get("TELEGRAM_ALERT_CHAT_ID")
        if not self.events or not token or not chat:
            return
        text = "🎧 add audio\n" + "\n".join(self.events)
        data = urllib.parse.urlencode({"chat_id": chat, "text": text}).encode()
        try:
            urllib.request.urlopen(f"https://api.telegram.org/bot{token}/sendMessage", data=data, timeout=15).close()
        except OSError as err:
            print(f"telegram failed: {err}", file=sys.stderr)


def load_state():
    try:
        return json.loads(STATE_FILE.read_text())
    except FileNotFoundError:
        return {"jobs": {}, "charged_bytes": 0}


def status(state, budget):
    print(f"budget {state.get('charged_bytes', 0) / GB:.1f}/{budget / GB:.0f} GB")
    for job in sorted(state["jobs"].values(), key=lambda j: j["requested"]):
        print(f"  {job['status']:11} {job['title']} [{job['lang']}]  {job.get('reason', '')}")


def main():
    parser = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    parser.add_argument("--status", action="store_true")
    parser.add_argument("--candidates", type=int, metavar="RADARR_ID")
    args = parser.parse_args()
    env = env_values()
    if args.candidates:
        run = Pass(env, {"jobs": {}})
        movie = http_json(f"{RADARR}/api/v3/movie/{args.candidates}", run.radarr)
        for c in run.find(movie["imdbId"], movie["title"], "spa"):
            print(f"{c['size'] / GB:5.1f} GB  {c['indexer']:14} {'private' if c['private'] else 'public':7} "
                  f"{'freeleech' if c['freeleech'] else '':9}  {c['title']}")
        return
    REQUESTS_DIR.mkdir(parents=True, exist_ok=True)
    if args.status:
        status(load_state(), float(env.get("DUB_FETCH_BUDGET_GB") or BUDGET_GB) * GB)
        return

    # Checks and jobs hold separate locks, so a check is answered while a job probes or add-audio runs.
    with open(JOBS_DIR / "check.lock", "w") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            Pass(env, {"jobs": {}}).run_checks(Pass(env, {"jobs": {}}).list_films())
        except BlockingIOError:
            pass

    with open(JOBS_DIR / "run.lock", "w") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return
        state = load_state()
        run = Pass(env, state)
        try:
            try:
                listed = {str(f["id"]): f for f in json.loads(FILMS_FILE.read_text())["films"]}
            except (OSError, ValueError):
                listed = run.list_films()
            run.take_requests(listed)
            active = sorted((j for j in state["jobs"].values() if j["status"] not in DONE), key=lambda j: j["requested"])
            if active:
                write_json(STATE_FILE, state)
                run.advance(active[0])
        finally:
            write_json(STATE_FILE, state)
            run.notify()


if __name__ == "__main__":
    main()
