#!/usr/bin/env python3
"""Inventory of the host ports every repo's containers take, failing when two claim the same one.

    scripts/ci/ports.py <repo dir> [<repo dir> ...]

Reads each repo through `docker compose config`, so it sees what compose would run: published
ports, plus the `host.ports` label that a container on the host network must carry, since compose
cannot know what such a container binds ("none" for one that listens on nothing). Prints the
table, also to the GitHub job summary.
"""
import json
import os
import subprocess
import sys

# Taken on the host outside Docker.
HOST_PROCESSES = [("tcp", 22, "", "sshd")]


def compose_config(repo):
    env_files = [f for f in ("versions.env", ".env" if os.path.exists(os.path.join(repo, ".env")) else ".env.example")
                 if os.path.exists(os.path.join(repo, f))]
    env = dict(os.environ, COMPOSE_ENV_FILES=",".join(env_files))
    r = subprocess.run(["docker", "compose", "--profile", "*", "config", "--format", "json"],
                       cwd=repo, env=env, capture_output=True, text=True)
    if r.returncode != 0:
        sys.exit(f"docker compose config failed in {repo}:\n{r.stderr}")
    return json.loads(r.stdout)


def repo_name(repo):
    url = subprocess.run(["git", "remote", "get-url", "origin"], cwd=repo, capture_output=True, text=True).stdout.strip()
    return os.path.basename(url).removesuffix(".git") or os.path.basename(os.path.abspath(repo))


def claims(repo):
    name = repo_name(repo)
    errors, out = [], []
    for svc, s in sorted((compose_config(repo).get("services") or {}).items()):
        who = f"{name}/{svc}"
        if s.get("network_mode") == "host":
            label = (s.get("labels") or {}).get("host.ports", "")
            if not label:
                errors.append(f"{who} is on the host network without a host.ports label")
            for item in label.split() if label != "none" else []:
                port, _, proto = item.partition("/")
                out.append((proto or "tcp", int(port), "", who))
        for p in s.get("ports") or []:
            if p.get("published"):
                out.append((p.get("protocol", "tcp"), int(p["published"]), p.get("host_ip", ""), who))
    return out, errors


def overlaps(ip_a, ip_b):
    return ip_a in ("", "0.0.0.0", "::") or ip_b in ("", "0.0.0.0", "::") or ip_a == ip_b


def main(repos):
    rows = list(HOST_PROCESSES)
    errors = []
    for repo in repos:
        r, e = claims(repo)
        rows += r
        errors += e
    rows.sort(key=lambda r: (r[1], r[0], r[3]))

    for i, a in enumerate(rows):
        for b in rows[i + 1:]:
            if a[:2] == b[:2] and a[3] != b[3] and overlaps(a[2], b[2]):
                errors.append(f"{a[1]}/{a[0]} is taken by both {a[3]} and {b[3]}")

    table = ["| Port | Bound to | Taken by |", "| ---: | :--- | :--- |"]
    table += [f"| {port}/{proto} | {ip or 'all interfaces'} | {who} |" for proto, port, ip, who in rows]
    report = "\n".join(table + [""] + [f"- {e}" for e in errors])
    print(report)
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as f:
            f.write("## Host ports\n\n" + report + "\n")
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:] or ["."]))
