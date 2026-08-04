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


def log(message):
    print(message, flush=True)


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
    subprocess.Popen([DEPLOY_SCRIPT], stdout=logfile, stderr=subprocess.STDOUT,
                     cwd=PROJECT_DIR, start_new_session=True)


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
            self.reply(413, "bad body\n")
            return

        body = self.rfile.read(length)
        if not signature_ok(self.headers.get("X-Hub-Signature-256"), body):
            log("rejected: bad or missing signature")
            self.reply(401, "unauthorized\n")
            return

        event = self.headers.get("X-GitHub-Event", "")
        if event == "ping":
            self.reply(200, "pong\n")
            return
        if event != "push":
            self.reply(200, f"ignored event {event}\n")
            return

        try:
            payload = json.loads(body)
        except ValueError:
            self.reply(400, "bad json\n")
            return

        repo = (payload.get("repository") or {}).get("name", "")
        ref = payload.get("ref", "")
        if repo not in ALLOWED_REPOS or ref != BRANCH_REF:
            log(f"ignored push: repo={repo!r} ref={ref!r}")
            self.reply(200, "ignored\n")
            return

        log(f"deploy triggered by push to {repo}")
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
