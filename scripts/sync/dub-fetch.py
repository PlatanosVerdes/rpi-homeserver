#!/usr/bin/env python3
"""Fetch a Castilian dub for library films that have none, one film at a time, and add it with add-audio.py.

    scripts/sync/dub-fetch.py             # one pass: process what finished, then grab the next film
    scripts/sync/dub-fetch.py --dry-run   # say what it would grab, change nothing
    scripts/sync/dub-fetch.py --status    # what it has done so far

Each pass, from cron once a day:
1. A source it grabbed earlier that has finished downloading goes through add-audio.py. Refused (no
   Castilian track, another cut) means the next pass tries the next smallest release of that film.
2. If nothing is still downloading and the budget allows, it searches the indexer for the next film
   without the language and grabs its smallest release, in qBittorrent category `audio-source`.
3. An `.original` it left behind is deleted a week later, once nothing else links to it.

The source torrent is never removed here: it is not hardlinked into the library, so qbit-manage tags
it noHL and its tracker's share-limit group seeds and deletes it. See docs/add-audio.md.
"""

import argparse
import fcntl
import hashlib
import http.cookiejar
import json
import os
import re
import subprocess
import sys
import time
import urllib.parse
import urllib.request
import uuid
from pathlib import Path

PROJECT_DIR = Path(os.path.expanduser("~/rpi-homeserver"))
HERE = Path(__file__).resolve().parent
ENV_FILE = PROJECT_DIR / ".env"
STATE_DIR = Path(os.environ.get("DUB_FETCH_STATE", PROJECT_DIR / "appdata" / "dub-fetch"))
STATE_FILE = STATE_DIR / "state.json"
DATA_ROOT = Path(os.environ.get("DATA_ROOT", "/mnt/data"))
CONTAINER_DATA = "/data"
RADARR = "http://127.0.0.1:7878"
PROWLARR = "http://127.0.0.1:9696"

LANG = "spa"
LANGUAGE_WORDS = {"spa", "spanish", "es", "esp", "español", "castellano"}
INDEXER = os.environ.get("DUB_FETCH_INDEXER", "NOBS")
CATEGORY = "audio-source"
SAVE_PATH = "/data/downloads"
REJECTED_RELEASES = re.compile(r"latin|\blat\b|m[eé]x", re.I)
VIDEO = (".mkv", ".mp4", ".m4v", ".avi")

GB = 1024**3
# A minimum ratio of 0.4 over a 50 GB signup credit leaves 125 GB of download before any upload counts.
BUDGET_GB = 100
MAX_SIZE_GB = 15
MAX_ATTEMPTS = 3
SEARCHES_PER_PASS = 5
STALLED_DAYS = 5
RETRY_NOT_FOUND_DAYS = 30
PRUNE_ORIGINAL_DAYS = 7
ADD_AUDIO_TIMEOUT = 4 * 3600
DAY = 86400


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


def http_json(url, headers=None, data=None, method=None, opener=None, timeout=120):
    request = urllib.request.Request(url, data=data, headers=headers or {}, method=method)
    with (opener.open if opener else urllib.request.urlopen)(request, timeout=timeout) as response:
        body = response.read()
    return json.loads(body) if body.strip() else None


def host_path(container_path):
    """Radarr and qBittorrent both mount DATA_ROOT at /data."""
    path = str(container_path)
    if path == CONTAINER_DATA or path.startswith(CONTAINER_DATA + "/"):
        return DATA_ROOT / path[len(CONTAINER_DATA) + 1:]
    return Path(path)


def has_language(audio_languages):
    words = {w.strip().lower() for w in re.split(r"[/,]", audio_languages or "")}
    return bool(words & LANGUAGE_WORDS)


def films_without_language(movies):
    films = []
    for movie in movies:
        file = movie.get("movieFile") or {}
        path = file.get("path", "")
        if not movie.get("hasFile") or not movie.get("imdbId") or not path.endswith(".mkv"):
            continue
        if has_language((file.get("mediaInfo") or {}).get("audioLanguages")):
            continue
        films.append({"id": movie["id"], "title": f"{movie['title']} ({movie.get('year')})",
                      "imdb": movie["imdbId"], "path": path, "added": file.get("dateAdded", "")})
    return sorted(films, key=lambda f: f["added"], reverse=True)


def is_freeleech(release):
    return "freeleech" in json.dumps(release.get("indexerFlags") or []).lower()


def charged_bytes(release):
    return 0 if is_freeleech(release) else release["size"]


def choose_release(results, imdb, tried):
    """The smallest live release of this film, freeleech first, never one already tried or Latin American."""
    number = int(imdb.lstrip("t") or 0)
    fits = [r for r in results
            if int(r.get("imdbId") or 0) == number and (r.get("seeders") or 0) > 0
            and 0 < r.get("size", 0) <= MAX_SIZE_GB * GB and r.get("guid") not in tried
            and not REJECTED_RELEASES.search(r.get("title", ""))]
    return min(fits, key=lambda r: (not is_freeleech(r), r["size"]), default=None)


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


def largest_video(path):
    if path.is_file():
        return path if path.suffix.lower() in VIDEO else None
    videos = [p for p in path.rglob("*") if p.suffix.lower() in VIDEO and p.is_file()] if path.is_dir() else []
    return max(videos, key=lambda p: p.stat().st_size, default=None)


def originals(target):
    return {p for p in target.parent.glob(target.name + ".original*")}


class Qbit:
    def __init__(self, env):
        self.base = f"http://127.0.0.1:{env.get('QBIT_WEBUI_PORT', '8080')}/api/v2"
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        login = urllib.parse.urlencode({"username": env.get("QBIT_USER", ""),
                                        "password": env.get("QBIT_PASSWORD", "")}).encode()
        with self.opener.open(urllib.request.Request(f"{self.base}/auth/login", data=login,
                                                     headers={"Referer": self.base}), timeout=30) as r:
            if r.read().strip() != b"Ok.":
                raise SystemExit("qBittorrent refused the login")

    def torrent(self, torrent_hash):
        found = http_json(f"{self.base}/torrents/info?hashes={torrent_hash}", opener=self.opener)
        return found[0] if found else None

    def ensure_category(self):
        if CATEGORY not in (http_json(f"{self.base}/torrents/categories", opener=self.opener) or {}):
            data = urllib.parse.urlencode({"category": CATEGORY, "savePath": SAVE_PATH}).encode()
            self.opener.open(urllib.request.Request(f"{self.base}/torrents/createCategory", data=data), timeout=30)

    def add(self, torrent, name):
        boundary = uuid.uuid4().hex
        parts = []
        for key, value in (("category", CATEGORY), ("tags", CATEGORY), ("savepath", SAVE_PATH),
                           ("autoTMM", "false")):
            parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{key}"\r\n\r\n{value}\r\n'.encode())
        parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="torrents"; filename="{name}.torrent"\r\n'
                     f"Content-Type: application/x-bittorrent\r\n\r\n".encode() + torrent + b"\r\n")
        parts.append(f"--{boundary}--\r\n".encode())
        request = urllib.request.Request(f"{self.base}/torrents/add", data=b"".join(parts),
                                         headers={"Content-Type": f"multipart/form-data; boundary={boundary}"})
        with self.opener.open(request, timeout=60) as r:
            if r.read().strip() != b"Ok.":
                raise RuntimeError("qBittorrent did not take the torrent")


class Pass:
    def __init__(self, env, state, dry_run):
        self.env, self.state, self.dry_run = env, state, dry_run
        self.radarr_headers = {"X-Api-Key": env.get("RADARR_API_KEY", "")}
        self.prowlarr_headers = {"X-Api-Key": env.get("PROWLARR_API_KEY", "")}
        self.events = []
        self._qbit = None

    @property
    def qbit(self):
        if self._qbit is None:
            self._qbit = Qbit(self.env)
        return self._qbit

    def say(self, line):
        print(line)
        self.events.append(line)

    def movie(self, movie_id):
        return http_json(f"{RADARR}/api/v3/movie/{movie_id}", self.radarr_headers)

    def process(self):
        """Run add-audio on every finished source; return whether one is still downloading."""
        downloading = False
        for key, film in self.state["films"].items():
            if film.get("status") != "fetching":
                continue
            attempt = film["attempts"][-1]
            torrent = self.qbit.torrent(attempt["hash"])
            if torrent is None:
                self.finish_attempt(film, "lost", "the torrent is no longer in qBittorrent")
                continue
            if torrent["progress"] < 1:
                if time.time() - attempt["added_at"] > STALLED_DAYS * DAY:
                    self.finish_attempt(film, "stalled", f"{torrent['progress']:.0%} after {STALLED_DAYS} days")
                else:
                    downloading = True
                continue
            if self.dry_run:
                print(f"would add the dub from {attempt['title']} to {film['title']}")
                continue
            self.add_audio(key, film, attempt, torrent)
        return downloading

    def add_audio(self, key, film, attempt, torrent):
        movie = self.movie(key)
        target = host_path((movie.get("movieFile") or {}).get("path", ""))
        source = largest_video(host_path(torrent["content_path"]))
        if not target.name.endswith(".mkv") or not target.exists():
            self.finish_attempt(film, "skipped", "the library file is gone or no longer .mkv", final=True)
            return
        if source is None:
            self.finish_attempt(film, "refused", "no video file in the download")
            return
        before = originals(target)
        try:
            run = subprocess.run([sys.executable, str(HERE / "add-audio.py"), str(target), str(source)],
                                 capture_output=True, text=True, timeout=ADD_AUDIO_TIMEOUT)
            output = (run.stdout + run.stderr).strip()
            code = run.returncode
        except subprocess.TimeoutExpired:
            output, code = "add-audio timed out", 1
        print(output)
        last = output.splitlines()[-1] if output else f"exit {code}"
        if code == 0:
            new = sorted(originals(target) - before)
            film["original"] = str(new[0]) if new else None
            self.finish_attempt(film, "added", last, final=True)
        elif "already has a" in last:
            self.finish_attempt(film, "skipped", "it already has the language", final=True)
        elif last.startswith("busy:"):
            film["status"] = "fetching"
            print(f"{film['title']}: {last}, next pass")
        else:
            self.finish_attempt(film, "refused", last)

    def finish_attempt(self, film, result, reason, final=False):
        attempt = film["attempts"][-1]
        attempt.update(result=result, reason=reason, finished_at=int(time.time()))
        if final or result == "added":
            film["status"] = "done" if result == "added" else result
        elif len(film["attempts"]) >= MAX_ATTEMPTS:
            film["status"] = "gave-up"
        else:
            film["status"] = "pending"
        film["updated"] = int(time.time())
        self.say(f"{film['title']}: {result} ({reason})")

    def prune_originals(self):
        for film in self.state["films"].values():
            original = film.get("original")
            if not original or film.get("status") != "done" or time.time() - film["updated"] < PRUNE_ORIGINAL_DAYS * DAY:
                continue
            path = Path(original)
            if not path.exists():
                film["original"] = None
            elif path.stat().st_nlink == 1 and not self.dry_run:
                size = path.stat().st_size
                path.unlink()
                film["original"] = None
                self.say(f"{film['title']}: deleted {path.name}, {size / GB:.1f} GB")

    def indexer_id(self):
        for indexer in http_json(f"{PROWLARR}/api/v1/indexer", self.prowlarr_headers) or []:
            if indexer.get("name") == INDEXER:
                return indexer["id"] if indexer.get("enable") else None
        return None

    def search(self, indexer, imdb):
        query = urllib.parse.urlencode({"query": f"{{ImdbId:{imdb}}}", "type": "movie",
                                        "indexerIds": indexer, "categories": 2000})
        return http_json(f"{PROWLARR}/api/v1/search?{query}", self.prowlarr_headers, timeout=180) or []

    def grab(self):
        spent = self.state.get("charged_bytes", 0)
        if spent >= BUDGET_GB * GB:
            print(f"budget spent: {spent / GB:.1f} of {BUDGET_GB:.0f} GB; raise DUB_FETCH_BUDGET_GB once the ratio allows")
            return
        indexer = self.indexer_id()
        if indexer is None:
            print(f"indexer {INDEXER} missing or disabled in Prowlarr; nothing grabbed")
            return
        movies = http_json(f"{RADARR}/api/v3/movie", self.radarr_headers) or []
        searches = 0
        for film in films_without_language(movies):
            entry = self.state["films"].setdefault(str(film["id"]), {"title": film["title"], "attempts": []})
            status = entry.get("status", "pending")
            if status in ("done", "gave-up", "skipped", "fetching"):
                continue
            if status == "not-found" and time.time() - entry.get("updated", 0) < RETRY_NOT_FOUND_DAYS * DAY:
                continue
            if searches >= SEARCHES_PER_PASS:
                break
            searches += 1
            tried = {a["guid"] for a in entry["attempts"]}
            release = choose_release(self.search(indexer, film["imdb"]), film["imdb"], tried)
            if release is None:
                entry.update(status="not-found", updated=int(time.time()))
                print(f"{film['title']}: no usable release on {INDEXER}")
                continue
            if spent + charged_bytes(release) > BUDGET_GB * GB:
                print(f"{film['title']}: {release['size'] / GB:.1f} GB would pass the budget; waiting")
                return
            if self.dry_run:
                print(f"would grab {release['title']} ({release['size'] / GB:.1f} GB) for {film['title']}")
                return
            self.fetch(entry, film, release)
            return

    def fetch(self, entry, film, release):
        url = urllib.parse.urlsplit(release["downloadUrl"])._replace(scheme="http", netloc="127.0.0.1:9696")
        with urllib.request.urlopen(urllib.parse.urlunsplit(url), timeout=60) as response:
            torrent = response.read()
        torrent_hash = infohash(torrent)
        self.qbit.ensure_category()
        self.qbit.add(torrent, film["title"])
        entry["attempts"].append({"guid": release["guid"], "title": release["title"], "hash": torrent_hash,
                                  "size": release["size"], "freeleech": is_freeleech(release),
                                  "added_at": int(time.time())})
        entry.update(status="fetching", updated=int(time.time()))
        self.state["charged_bytes"] = self.state.get("charged_bytes", 0) + charged_bytes(release)
        self.say(f"{film['title']}: grabbed {release['title']}, {release['size'] / GB:.1f} GB "
                 f"(budget {self.state['charged_bytes'] / GB:.1f}/{BUDGET_GB:.0f} GB)")

    def notify(self):
        token, chat = self.env.get("TELEGRAM_ALERT_BOT_TOKEN"), self.env.get("TELEGRAM_ALERT_CHAT_ID")
        if not self.events or self.dry_run or not token or not chat:
            return
        text = "🎧 dub-fetch\n" + "\n".join(self.events)
        data = urllib.parse.urlencode({"chat_id": chat, "text": text}).encode()
        try:
            urllib.request.urlopen(f"https://api.telegram.org/bot{token}/sendMessage", data=data, timeout=15).close()
        except OSError as err:
            print(f"telegram failed: {err}", file=sys.stderr)


def load_state():
    try:
        return json.loads(STATE_FILE.read_text())
    except FileNotFoundError:
        return {"films": {}, "charged_bytes": 0}


def save_state(state):
    tmp = STATE_FILE.with_suffix(".tmp")
    tmp.write_text(json.dumps(state, indent=1, sort_keys=True))
    tmp.replace(STATE_FILE)


def status(state):
    counts = {}
    for film in state["films"].values():
        counts[film.get("status", "pending")] = counts.get(film.get("status", "pending"), 0) + 1
    print(f"budget {state.get('charged_bytes', 0) / GB:.1f}/{BUDGET_GB:.0f} GB  " +
          "  ".join(f"{k} {v}" for k, v in sorted(counts.items())))
    for film in sorted(state["films"].values(), key=lambda f: f.get("updated", 0)):
        last = film["attempts"][-1] if film["attempts"] else {}
        print(f"  {film.get('status', 'pending'):10} {film['title']}  {last.get('reason') or last.get('title', '')}")


def main():
    global BUDGET_GB
    env = env_values()
    BUDGET_GB = float(env.get("DUB_FETCH_BUDGET_GB") or BUDGET_GB)
    parser = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--status", action="store_true")
    args = parser.parse_args()
    STATE_DIR.mkdir(parents=True, exist_ok=True)
    if args.status:
        status(load_state())
        return
    with open(STATE_DIR / "run.lock", "w") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise SystemExit("another pass is running")
        state = load_state()
        run = Pass(env, state, args.dry_run)
        print(time.strftime("%Y-%m-%d %H:%M:%S"), "dub-fetch", "(dry run)" if args.dry_run else "")
        try:
            downloading = run.process()
            run.prune_originals()
            if not downloading:
                run.grab()
        finally:
            if not args.dry_run:
                save_state(state)
            run.notify()


if __name__ == "__main__":
    main()
