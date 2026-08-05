#!/usr/bin/env python3
"""GitHub push webhook receiver: verifies the HMAC signature, then runs deploy_control.sh.

Listens on 127.0.0.1 only. The public entrypoint is the Cloudflare tunnel (see
compose-core.yml `cloudflared` and docs/deploy-webhook.md). The endpoint is reachable
by anyone on the internet, so an unsigned request must never reach the deploy script.
"""

import hashlib
import hmac
import json
import os
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PROJECT_DIR = os.path.expanduser("~/rpi-homeserver")
DEPLOY_SCRIPT = f"{PROJECT_DIR}/scripts/deploy_control.sh"
DEPLOY_LOG = f"{PROJECT_DIR}/deploy_control.log"
ALLOWED_REPOS = {"rpi-homeserver", "rpi-services"}
BRANCH_REF = "refs/heads/main"
MAX_BODY = 1 << 20


def read_env(path):
    """Minimal KEY=VALUE parser for .env; real environment variables win."""
    values = {}
    try:
        with open(path) as fh:
            for line in fh:
                line = line.strip()
                if not line or line.startswith("#") or "=" not in line:
                    continue
                key, _, value = line.partition("=")
                values[key.strip()] = value.strip().strip('"').strip("'")
    except OSError:
        pass
    return values


ENV = read_env(f"{PROJECT_DIR}/.env")
SECRET = (os.environ.get("GITHUB_WEBHOOK_SECRET") or ENV.get("GITHUB_WEBHOOK_SECRET", "")).encode()
HOOK_PATH = os.environ.get("WEBHOOK_PATH") or ENV.get("WEBHOOK_PATH") or "/hooks/deploy"
PORT = int(os.environ.get("WEBHOOK_PORT") or ENV.get("WEBHOOK_PORT") or 9000)


PUSHGATEWAY = os.environ.get("PUSHGATEWAY_URL") or "http://localhost:9091"
COUNTS = {"accepted": 0, "rejected": 0, "ignored": 0}
LAST_ACCEPTED = 0
METRICS_LOCK = threading.Lock()


def log(message):
    print(message, flush=True)


def record(result):
    """Count the request and push to Pushgateway.

    `rejected` is the interesting one: the endpoint is public, so a rising count means someone
    is probing it. Counters restart at 0 when the service restarts, which Prometheus handles.
    """
    global LAST_ACCEPTED
    with METRICS_LOCK:
        COUNTS[result] = COUNTS.get(result, 0) + 1
        if result == "accepted":
            LAST_ACCEPTED = int(time.time())
        body = [
            "# HELP webhook_requests_total Requests to the deploy webhook by outcome",
            "# TYPE webhook_requests_total counter",
            *(f'webhook_requests_total{{result="{k}"}} {v}' for k, v in COUNTS.items()),
            "# HELP webhook_last_accepted_timestamp When a valid push last triggered a deploy",
            "# TYPE webhook_last_accepted_timestamp gauge",
            f"webhook_last_accepted_timestamp {LAST_ACCEPTED}",
            "",
        ]
    # Off the request path: GitHub gives the hook 10s to answer, and a stuck Pushgateway would
    # otherwise spend that budget on a metric nobody is waiting for.
    threading.Thread(target=_push, args=("\n".join(body),), daemon=True).start()


def _push(payload):
    request = urllib.request.Request(f"{PUSHGATEWAY}/metrics/job/deploy_webhook",
                                    data=payload.encode(), method="POST")
    try:
        urllib.request.urlopen(request, timeout=5).close()
    except (urllib.error.URLError, OSError) as exc:
        log(f"pushgateway unreachable: {exc}")


def signature_ok(header, body):
    if not header or not header.startswith("sha256="):
        return False
    digest = hmac.new(SECRET, body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(digest, header[len("sha256="):])


def trigger_deploy():
    """Fire and forget: deploy takes minutes, GitHub times out after 10 seconds.

    deploy_control.sh holds an flock, so overlapping pushes cannot run two deploys.
    """
    logfile = open(DEPLOY_LOG, "a")
    env = {**os.environ, "DEPLOY_TRIGGER": "webhook"}
    subprocess.Popen([DEPLOY_SCRIPT], stdout=logfile, stderr=subprocess.STDOUT,
                     cwd=PROJECT_DIR, env=env, start_new_session=True)


class Handler(BaseHTTPRequestHandler):
    server_version = "deploy-webhook"
    sys_version = ""

    def reply(self, code, message="ok\n"):
        body = message.encode()
        self.send_response(code)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt, *args):
        log(f"{self.address_string()} {fmt % args}")

    def do_GET(self):
        self.reply(404, "not found\n")

    def do_POST(self):
        if self.path != HOOK_PATH:
            self.reply(404, "not found\n")
            return

        length = int(self.headers.get("Content-Length") or 0)
        if length <= 0 or length > MAX_BODY:
            record("rejected")
            self.reply(413, "bad body\n")
            return

        body = self.rfile.read(length)
        if not signature_ok(self.headers.get("X-Hub-Signature-256"), body):
            log("rejected: bad or missing signature")
            record("rejected")
            self.reply(401, "unauthorized\n")
            return

        event = self.headers.get("X-GitHub-Event", "")
        if event == "ping":
            record("ignored")
            self.reply(200, "pong\n")
            return
        if event != "push":
            record("ignored")
            self.reply(200, f"ignored event {event}\n")
            return

        try:
            payload = json.loads(body)
        except ValueError:
            record("rejected")
            self.reply(400, "bad json\n")
            return

        repo = (payload.get("repository") or {}).get("name", "")
        ref = payload.get("ref", "")
        if repo not in ALLOWED_REPOS or ref != BRANCH_REF:
            log(f"ignored push: repo={repo!r} ref={ref!r}")
            record("ignored")
            self.reply(200, "ignored\n")
            return

        log(f"deploy triggered by push to {repo}")
        record("accepted")
        self.reply(202, "deploying\n")
        trigger_deploy()


def main():
    if not SECRET:
        sys.exit("GITHUB_WEBHOOK_SECRET is not set (add it to .env)")
    if not os.access(DEPLOY_SCRIPT, os.X_OK):
        sys.exit(f"{DEPLOY_SCRIPT} is missing or not executable")
    log(f"listening on 127.0.0.1:{PORT}{HOOK_PATH}")
    ThreadingHTTPServer(("127.0.0.1", PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()
