"""The numbers no exporter provides, served instead of pushed.

Each collector runs on its own schedule in the background and every scrape answers the last
snapshot, in milliseconds. That is not an optimisation: one media pass takes ~7s against five APIs,
and collecting on demand would both risk Prometheus's 10s scrape timeout and ask those APIs twenty
times more often than the every-five-minutes they need.

So `up` and freshness are two separate questions, and both are answerable: `up` says the process
lives, pi_metrics_last_success_timestamp_seconds says the data behind a panel is recent.
"""

import http.server
import os
import threading
import time

import host
import media

PORT = int(os.environ.get("PORT", "9110"))
MEDIA_EVERY = int(os.environ.get("MEDIA_INTERVAL", "300"))
DISK_EVERY = int(os.environ.get("DISK_INTERVAL", "3600"))

lock = threading.Lock()
snapshot = {}          # collector -> lines
meta = {}              # collector -> (last success unix, seconds it took, failure count)
every = {}             # collector -> its interval, so an alert can say "overdue" on its own


def run(name, produce, interval):
    every[name] = interval
    while True:
        started = time.time()
        try:
            lines = produce()
            problems = []
            if isinstance(lines, tuple):
                lines, problems = lines
            with lock:
                snapshot[name] = lines
                meta[name] = (int(started), time.time() - started, len(problems))
            for problem in problems:
                print(f"{name}: {problem}", flush=True)
        except Exception as exc:                       # noqa: BLE001 - a collector, not the service
            print(f"{name}: {exc}", flush=True)
            with lock:
                meta[name] = (meta.get(name, (0, 0, 0))[0], time.time() - started, 1)
        time.sleep(interval)


def body():
    with lock:
        parts = [snapshot.get(name, "") for name in ("media", "disk")]
        current = dict(meta)
    # zram is one sysfs read, so it is answered live rather than cached.
    try:
        parts.append("\n".join(host.zram()))
    except Exception as exc:                           # noqa: BLE001
        print(f"zram: {exc}", flush=True)

    out = [
        "# HELP pi_metrics_last_success_timestamp_seconds When this collector last produced a"
        " snapshot",
        "# TYPE pi_metrics_last_success_timestamp_seconds gauge",
        "# HELP pi_metrics_collection_seconds How long its last run took",
        "# TYPE pi_metrics_collection_seconds gauge",
        "# HELP pi_metrics_collection_failures Groups that failed in its last run",
        "# TYPE pi_metrics_collection_failures gauge",
        "# HELP pi_metrics_collection_interval_seconds How often it is supposed to run, so overdue"
        " is answerable without knowing the schedule",
        "# TYPE pi_metrics_collection_interval_seconds gauge",
    ]
    for name, (last, took, failed) in sorted(current.items()):
        out += [
            f'pi_metrics_last_success_timestamp_seconds{{collector="{name}"}} {last}',
            f'pi_metrics_collection_seconds{{collector="{name}"}} {took:.3f}',
            f'pi_metrics_collection_failures{{collector="{name}"}} {failed}',
            f'pi_metrics_collection_interval_seconds{{collector="{name}"}} {every.get(name, 0)}',
        ]
    parts.append("\n".join(out))
    return "\n".join(part for part in parts if part).rstrip("\n") + "\n"


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_GET(self):
        if self.path.rstrip("/") not in ("", "/metrics"):
            self.send_error(404)
            return
        payload = body().encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, *_args):
        pass                                            # a line per scrape is four an hour of noise


if __name__ == "__main__":
    for name, produce, interval in (("media", media.collect, MEDIA_EVERY),
                                    ("disk", lambda: "\n".join(host.disk()), DISK_EVERY)):
        threading.Thread(target=run, args=(name, produce, interval), daemon=True).start()
    print(f"pi-metrics on :{PORT}, media every {MEDIA_EVERY}s, disk every {DISK_EVERY}s", flush=True)
    http.server.ThreadingHTTPServer(("", PORT), Handler).serve_forever()
