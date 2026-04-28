#!/usr/bin/env python3
"""
Replacement for `bws run --` that also expands JSON-valued secrets.

Usage: bws-run.py <command> [args...]

Reads BWS_ACCESS_TOKEN from environment, fetches all secrets in the project,
and for each secret:
  - If value is a JSON object  → expands each key/value as a separate env var
  - If value is a plain string → uses secret key = value directly

Then exec's the given command with the enriched environment.
"""

import json
import os
import subprocess
import sys


def load_secrets(token: str) -> dict[str, str]:
    result = subprocess.run(
        ["bws", "secret", "list"],
        capture_output=True, text=True,
        env={**os.environ, "BWS_ACCESS_TOKEN": token},
        timeout=30,
    )
    if result.returncode != 0:
        print(f"bws-run: error fetching secrets: {result.stderr.strip()}", file=sys.stderr)
        sys.exit(1)

    secrets = json.loads(result.stdout)
    env_vars: dict[str, str] = {}

    for secret in secrets:
        key = secret["key"]
        value = secret["value"]
        try:
            obj = json.loads(value)
            if isinstance(obj, dict):
                for k, v in obj.items():
                    if isinstance(v, str):
                        env_vars[k] = v
                    else:
                        env_vars[k] = json.dumps(v)
            else:
                env_vars[key] = value
        except (json.JSONDecodeError, TypeError):
            env_vars[key] = value

    return env_vars


def main():
    if len(sys.argv) < 2:
        print(f"Usage: {sys.argv[0]} <command> [args...]", file=sys.stderr)
        sys.exit(1)

    token = os.environ.get("BWS_ACCESS_TOKEN", "")
    if not token:
        print("bws-run: BWS_ACCESS_TOKEN not set", file=sys.stderr)
        sys.exit(1)

    secrets = load_secrets(token)
    env = {**os.environ, **secrets}

    os.execvpe(sys.argv[1], sys.argv[1:], env)


if __name__ == "__main__":
    main()
