#!/usr/bin/env python3
"""The images this repo builds from services/, and the versions.env pins that deploy them.

    scripts/ci/service-images.py plan [--base <commit>] [--offline]
    scripts/ci/service-images.py pin [--dry-run] [--offline] [--message <file>] < plan.json

A service here is one that compose builds from a directory of this repo, so the list comes from
`docker compose config` and a new directory needs no entry anywhere. One whose compose entry also
has `image: ghcr.io/platanosverdes/<name>:${VAR}` is published and pinned through VAR; one without
it is only built, to prove it still builds, because nothing would pull it.

`plan` prints, as JSON, the services whose image is due: something in the build context or in the
resolved build config (Dockerfile, build args from versions.env) changed since the commit the
pinned tag was built from, or that tag is not published. Comparing against the pinned tag and not
the previous push is what makes a failed or cancelled run catch up on the next one. Services with
no pin compare against --base, and are always built without it.

`pin` reads that JSON and writes each published tag into its VAR in versions.env, unless the pin
already there was built from a later commit.
"""
import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile

REGISTRY = "ghcr.io/platanosverdes/"
IMAGE_LINE = re.compile(r"^\s*image:\s*[\"']?(" + re.escape(REGISTRY) + r"[a-z0-9._/-]+):\$\{([A-Z0-9_]+)\}", re.M)
# v<commit date>-<commit>: sorts by date, and names the commit the next plan compares against.
TAG = re.compile(r"^v\d{4}\.\d{2}\.\d{2}-([0-9a-f]{7,40})$")


def git(*args, cwd=None, check=True):
    r = subprocess.run(["git", *args], cwd=cwd, capture_output=True, text=True)
    if check and r.returncode != 0:
        sys.exit(f"git {' '.join(args)} failed:\n{r.stderr}")
    return r


def compose_config(repo, env_file):
    env = dict(os.environ, COMPOSE_ENV_FILES=f"versions.env,{env_file}")
    r = subprocess.run(["docker", "compose", "--profile", "*", "config", "--format", "json"],
                       cwd=repo, env=env, capture_output=True, text=True)
    if r.returncode != 0:
        return None, r.stderr
    return json.loads(r.stdout), ""


def env_file(repo):
    return ".env" if os.path.exists(os.path.join(repo, ".env")) else ".env.example"


def image_vars(repo):
    found = {}
    for name in sorted(os.listdir(repo)):
        if name == "docker-compose.yml" or re.fullmatch(r"compose-[^/]*\.yml", name):
            with open(os.path.join(repo, name)) as f:
                found.update(IMAGE_LINE.findall(f.read()))
    return found


def built_services(config, repo):
    root = os.path.realpath(repo)
    out = {}
    for name, svc in sorted((config.get("services") or {}).items()):
        build = svc.get("build") or {}
        context = build.get("context")
        if not context or "://" in context:
            continue
        rel = os.path.relpath(os.path.realpath(context), root)
        if rel.startswith(".."):
            continue
        out[name] = {
            "context": rel,
            "dockerfile": build.get("dockerfile") or "Dockerfile",
            "args": {k: "" if v is None else str(v) for k, v in sorted((build.get("args") or {}).items())},
            "target": build.get("target") or "",
            "image": svc.get("image") or "",
        }
    return out


def commit_date(commit):
    r = subprocess.run(["git", "show", "-s", "--format=%cd", "--date=format-local:%Y.%m.%d", commit],
                       capture_output=True, text=True, env=dict(os.environ, TZ="UTC"), check=True)
    return r.stdout.strip()


def published(ref):
    return subprocess.run(["docker", "buildx", "imagetools", "inspect", ref],
                          capture_output=True, text=True).returncode == 0


class Snapshots:
    """The built services as compose resolved them at an older commit."""

    def __init__(self, repo):
        self.repo = repo
        self.cache = {}

    def at(self, commit):
        if commit not in self.cache:
            tmp = tempfile.mkdtemp()
            try:
                archive = subprocess.run(["git", "archive", commit], cwd=self.repo, capture_output=True)
                subprocess.run(["tar", "-x", "-C", tmp], input=archive.stdout, check=True)
                ef = env_file(self.repo)
                shutil.copy(os.path.join(self.repo, ef), os.path.join(tmp, ef))
                config, _ = compose_config(tmp, ef)
                self.cache[commit] = built_services(config, tmp) if config else None
            finally:
                shutil.rmtree(tmp, ignore_errors=True)
        return self.cache[commit]


def build_signature(svc):
    return {k: svc[k] for k in ("context", "dockerfile", "args", "target")}


def plan(args):
    repo = os.getcwd()
    config, err = compose_config(repo, env_file(repo))
    if config is None:
        sys.exit(f"docker compose config failed:\n{err}")
    head = git("rev-parse", "HEAD").stdout.strip()
    tag = f"v{commit_date(head)}-{head[:7]}"
    var_of = image_vars(repo)
    snapshots = Snapshots(repo)
    due = []
    for name, svc in built_services(config, repo).items():
        repo_ref, _, pinned = svc["image"].rpartition(":") if svc["image"] else ("", "", "")
        var = var_of.get(repo_ref, "")
        publish = bool(var)
        base, reason = None, ""
        if publish:
            m = TAG.match(pinned)
            if not m:
                reason = f"pinned to {pinned!r}, which this workflow did not build"
            elif git("rev-parse", "-q", "--verify", m.group(1) + "^{commit}", check=False).returncode != 0:
                reason = f"pinned tag {pinned} names a commit this history does not have"
            else:
                base = git("rev-parse", m.group(1) + "^{commit}").stdout.strip()
                if not args.offline and not published(svc["image"]):
                    reason = f"{svc['image']} is not published"
        elif args.base:
            base = args.base
        else:
            reason = "no pin to compare against"
        if base and not reason:
            files = git("diff", "--name-only", base, head, "--", svc["context"] + "/").stdout.split()
            before = snapshots.at(base)
            if files:
                reason = "changed: " + ", ".join(files[:5]) + (" ..." if len(files) > 5 else "")
            elif before is None or name not in before:
                reason = f"not in the compose config at {base[:7]}"
            elif build_signature(before[name]) != build_signature(svc):
                reason = "build config changed (Dockerfile path or build args)"
        if not reason:
            continue
        due.append({
            "service": name,
            "context": svc["context"],
            "dockerfile": os.path.join(svc["context"], svc["dockerfile"]),
            "build_args": "\n".join(f"{k}={v}" for k, v in svc["args"].items()),
            "target": svc["target"],
            "image": repo_ref if publish else "",
            "var": var,
            "tag": tag,
            "commit": head,
            "reason": reason,
        })
    print(json.dumps(due))
    summary(due)


def summary(due):
    rows = ["| Service | Image | Why |", "| :--- | :--- | :--- |"]
    rows += [f"| {d['service']} | {d['image'] + ':' + d['tag'] if d['image'] else 'built only, no image: line'} | {d['reason']} |"
             for d in due]
    text = "\n".join(rows) if due else "No service image is due."
    print(text, file=sys.stderr)
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as f:
            f.write("## Service images\n\n" + text + "\n")


def pin(args):
    due = json.load(sys.stdin)
    with open("versions.env") as f:
        lines = f.read().splitlines(keepends=True)
    pinned, unpublished = [], False
    for d in due:
        if not d["image"]:
            continue
        ref = f"{d['image']}:{d['tag']}"
        if not args.offline and not published(ref):
            print(f"::error::{ref} cannot be pulled anonymously: its build failed, or the package is not public yet")
            unpublished = True
            continue
        for i, line in enumerate(lines):
            key, sep, current = line.rstrip("\n").partition("=")
            if key != d["var"] or not sep:
                continue
            m = TAG.match(current)
            if current == d["tag"]:
                print(f"{d['var']} is already on {current}")
            elif m and git("merge-base", "--is-ancestor", d["commit"], m.group(1), check=False).returncode == 0:
                print(f"{d['var']} is already on {current}, built from a later commit; left as it is")
            else:
                lines[i] = f"{key}={d['tag']}\n"
                pinned.append(d["service"])
                print(f"{d['var']}={d['tag']}")
            break
        else:
            sys.exit(f"::error::{d['var']} is not in versions.env")
    if pinned and not args.dry_run:
        with open("versions.env", "w") as f:
            f.writelines(lines)
        if args.message:
            with open(args.message, "w") as f:
                f.write(f"Deploy {', '.join(pinned)} {due[0]['tag']}\n")
    if not pinned:
        print("Nothing to pin.")
    # 3 and not 1: what was published is still pinned, and the caller commits it before failing.
    sys.exit(3 if unpublished else 0)


def main():
    p = argparse.ArgumentParser()
    sub = p.add_subparsers(dest="cmd", required=True)
    pp = sub.add_parser("plan")
    pp.add_argument("--base", help="commit to compare services without a pin against")
    pp.add_argument("--offline", action="store_true", help="do not ask the registry what is published")
    pn = sub.add_parser("pin")
    pn.add_argument("--dry-run", action="store_true", help="print the new pins, write nothing")
    pn.add_argument("--message", help="file to write the deploy commit message into")
    pn.add_argument("--offline", action="store_true")
    args = p.parse_args()
    plan(args) if args.cmd == "plan" else pin(args)


if __name__ == "__main__":
    main()
