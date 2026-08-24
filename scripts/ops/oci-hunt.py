#!/usr/bin/env python3
"""Keep asking Oracle for the free instance until capacity exists, then say so on Telegram.

Oracle's Always Free shapes in a single-AD region are permanently oversubscribed: creating one is a
race against everyone else in the region, with no way to know in advance (Capacity Reports are not
available on a free tenancy). So this asks on a schedule instead, tries both free shapes, and stops
the moment one lands. It is idempotent on purpose: if the instance already exists, it exits without
doing anything, so it is safe to run from cron forever.

Signing is done by hand rather than with the OCI SDK: the SDK is a 50 MB dependency for four API
calls, and the signature is a documented HTTP Signature over a handful of headers.
"""

import base64
import datetime
import email.utils
import hashlib
import json
import os
import sys
import urllib.parse
import urllib.request

from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import padding

HOME = os.path.expanduser("~/rpi-homeserver")
KEY_FILE = f"{HOME}/appdata/oci/api_key.pem"
STATE_FILE = f"{HOME}/appdata/oci/state.json"
ENV_FILE = f"{HOME}/.env"

USER = "ocid1.user.oc1..aaaaaaaasjauzdfkxwxx2vo2np5v5zaxczwljcwvh37jola5gbx2c7idlm7a"
TENANCY = "ocid1.tenancy.oc1..aaaaaaaaq4cbn7clmh7a2bijvysc46ii3gkhbqw4qgcwhbd7bfskaaqalpeq"
FINGERPRINT = "d2:e8:9c:64:45:82:76:53:2d:5b:1f:30:0e:e0:e0:5f"
REGION = "eu-madrid-1"

NAME = "seedgate"
SSH_KEY = ("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBYtwcWMwV0yRrYqcr9t79jwa9UUlhVOcPXhU6gJQ6Lv"
           " homelab")

# Both free shapes, tried in this order. The ARM one is the better machine; the micro is the
# fallback. memoryInGBs/ocpus are ignored for the fixed micro shape.
SHAPES = [
    {"shape": "VM.Standard.A1.Flex", "ocpus": 1, "memory": 6, "arch": "aarch64"},
    {"shape": "VM.Standard.E2.1.Micro", "ocpus": None, "memory": None, "arch": "x86_64"},
]

IAAS = f"https://iaas.{REGION}.oraclecloud.com"
IDENTITY = f"https://identity.{REGION}.oraclecloud.com"

# Oracle answers a full region with "Out of host capacity" as a 500, which is not an error worth
# reporting: it is the normal answer to this question.
CAPACITY_MARKERS = ("out of host capacity", "out of capacity")

# And it rate-limits the asking, not just the capacity: launch attempts return 429 once they come
# too close together. Measured directly on 2026-08-24 by driving launch() at fixed spacings with the
# cron stopped: exactly one call gets through per ~60 s, no matter how hard you ask. Asking every
# 20 s for 15 minutes let 15 through, one every 61 s; 120/90/60 s spacings were all clean, while
# 45 s alternated (its first opportunity past the limit is 90 s) and 30 s let one in two through,
# again one every 61 s. So the ceiling is a flat rate, not a burst budget that runs out.
#
# One call per run, so the cron interval is the rate: the ARM shape every run because it is the one
# worth having, the micro only every Nth run as a fallback.
MICRO_EVERY = 5

# Backoff is per shape, and deliberately barely longer than the measured limit. A 429 costs Oracle
# nothing and means only "not yet", so the old 2-to-30-minute ladder punished us far harder than
# Oracle does: at one call per ~60 s, a shape sitting out 30 minutes throws away 29 turns it was
# entitled to. One minute is exactly one skipped run, which is all the limit asks for.
#
# Whether the limit is per shape or per user is NOT settled: the probe only ever asked for the ARM.
# The one cross-shape data point leans user-wide (a micro call 21 s after an ARM call was refused),
# so do not assume asking for both shapes buys two calls a minute.
BACKOFF_MINUTES = 1
BACKOFF_MAX = 4


def private_key():
    with open(KEY_FILE, "rb") as handle:
        return serialization.load_pem_private_key(handle.read(), password=None)


def call(base, method, path, body=None, query=None):
    if query:
        path = path + "?" + urllib.parse.urlencode(query)
    host = urllib.parse.urlparse(base).netloc
    now = email.utils.format_datetime(datetime.datetime.now(datetime.timezone.utc), usegmt=True)

    signed = [f"(request-target): {method.lower()} {path}", f"date: {now}", f"host: {host}"]
    headers = {"date": now, "host": host}
    data = None
    if body is not None:
        data = json.dumps(body).encode()
        digest = base64.b64encode(hashlib.sha256(data).digest()).decode()
        signed += [f"x-content-sha256: {digest}", "content-type: application/json",
                   f"content-length: {len(data)}"]
        headers.update({"x-content-sha256": digest, "content-type": "application/json",
                        "content-length": str(len(data))})

    signature = base64.b64encode(private_key().sign(
        "\n".join(signed).encode(), padding.PKCS1v15(), hashes.SHA256())).decode()
    names = " ".join(line.split(":")[0] for line in signed)
    headers["authorization"] = (
        f'Signature version="1",keyId="{TENANCY}/{USER}/{FINGERPRINT}",'
        f'algorithm="rsa-sha256",headers="{names}",signature="{signature}"')

    request = urllib.request.Request(base + path, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return response.status, json.loads(response.read() or "null")
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode(errors="ignore")
        try:
            return exc.code, json.loads(raw)
        except ValueError:
            return exc.code, {"message": raw[:300]}


def telegram(text):
    values = {}
    with open(ENV_FILE) as handle:
        for line in handle:
            if "=" in line and not line.startswith("#"):
                key, _, value = line.strip().partition("=")
                values[key] = value.strip('"').strip("'")
    token = values.get("TELEGRAM_ALERT_BOT_TOKEN")
    chat = values.get("TELEGRAM_ALERT_CHAT_ID")
    if not token or not chat:
        print("no telegram credentials in .env", file=sys.stderr)
        return
    data = urllib.parse.urlencode({"chat_id": chat, "text": text,
                                   "parse_mode": "HTML"}).encode()
    try:
        urllib.request.urlopen(f"https://api.telegram.org/bot{token}/sendMessage",
                               data=data, timeout=15).close()
    except Exception as exc:
        print(f"telegram failed: {exc}", file=sys.stderr)


def state():
    try:
        with open(STATE_FILE) as handle:
            return json.load(handle)
    except (OSError, ValueError):
        return {}


def save_state(data):
    with open(STATE_FILE, "w") as handle:
        json.dump(data, handle, indent=2)


def discover():
    """The AD, the public subnet and one image per architecture. Cached: none of it moves."""
    cached = state()
    if cached.get("subnet") and cached.get("images") and cached.get("ad"):
        return cached

    status, ads = call(IDENTITY, "GET", "/20160918/availabilityDomains",
                       query={"compartmentId": TENANCY})
    if status != 200:
        sys.exit(f"cannot list availability domains: {status} {ads}")
    cached["ad"] = ads[0]["name"]

    status, subnets = call(IAAS, "GET", "/20160918/subnets", query={"compartmentId": TENANCY})
    if status != 200:
        sys.exit(f"cannot list subnets: {status} {subnets}")
    public = [s for s in subnets if not s.get("prohibitPublicIpOnVnic")]
    if not public:
        sys.exit("no public subnet found, create one first")
    cached["subnet"] = public[0]["id"]
    cached["subnet_name"] = public[0]["displayName"]

    cached["images"] = {}
    for entry in SHAPES:
        status, images = call(IAAS, "GET", "/20160918/images", query={
            "compartmentId": TENANCY, "operatingSystem": "Canonical Ubuntu",
            "operatingSystemVersion": "24.04", "shape": entry["shape"],
            "sortBy": "TIMECREATED", "sortOrder": "DESC", "limit": 5})
        if status != 200 or not images:
            print(f"no image for {entry['shape']}: {status} {images}", file=sys.stderr)
            continue
        # Minimal images are fine but the standard one matches what a human would have picked.
        pick = next((i for i in images if "Minimal" not in i["displayName"]), images[0])
        cached["images"][entry["shape"]] = pick["id"]
        cached.setdefault("image_names", {})[entry["shape"]] = pick["displayName"]

    save_state(cached)
    return cached


def existing():
    status, instances = call(IAAS, "GET", "/20160918/instances", query={"compartmentId": TENANCY})
    if status != 200:
        return None
    return [i for i in instances
            if i["displayName"] == NAME and i["lifecycleState"] not in ("TERMINATED", "TERMINATING")]


def public_ip(instance_id):
    status, attachments = call(IAAS, "GET", "/20160918/vnicAttachments",
                               query={"compartmentId": TENANCY, "instanceId": instance_id})
    if status != 200:
        return None
    for attachment in attachments:
        if not attachment.get("vnicId"):
            continue
        status, vnic = call(IAAS, "GET", f"/20160918/vnics/{attachment['vnicId']}")
        if status == 200 and vnic.get("publicIp"):
            return vnic["publicIp"]
    return None


def launch(config, entry):
    image = config["images"].get(entry["shape"])
    if not image:
        return None, "no image"
    body = {
        "availabilityDomain": config["ad"],
        "compartmentId": TENANCY,
        "displayName": NAME,
        "shape": entry["shape"],
        "sourceDetails": {"sourceType": "image", "imageId": image},
        "createVnicDetails": {"subnetId": config["subnet"], "assignPublicIp": True},
        "metadata": {"ssh_authorized_keys": SSH_KEY},
    }
    if entry["ocpus"]:
        body["shapeConfig"] = {"ocpus": entry["ocpus"], "memoryInGBs": entry["memory"]}
    status, answer = call(IAAS, "POST", "/20160918/instances", body=body)
    if status in (200, 201):
        return answer, None
    return None, f"{status}: {(answer or {}).get('message', answer)}"


def cooling(shape):
    until = state().get("cooldowns", {}).get(shape)
    if not until:
        return False
    return datetime.datetime.now(datetime.timezone.utc) < datetime.datetime.fromisoformat(until)


def start_backoff(shape):
    data = state()
    throttles = data.setdefault("throttles", {})
    count = throttles.get(shape, 0) + 1
    throttles[shape] = count
    minutes = min(BACKOFF_MINUTES * (2 ** (count - 1)), BACKOFF_MAX)
    until = datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(minutes=minutes)
    data.setdefault("cooldowns", {})[shape] = until.isoformat()
    save_state(data)
    return minutes


def clear_throttles(shape):
    data = state()
    if data.get("throttles", {}).get(shape):
        data["throttles"][shape] = 0
        save_state(data)


def tally(kind):
    """Count the run instead of narrating it, and print one summary line per hour.

    Every ordinary run says exactly the same thing, so at one ask a minute the old line-per-run was
    1440 identical lines a day in the log store, drowning the ones that mean something. Counters
    live in the state file, so the summary survives across runs; it prints on the first run of the
    next hour, since nothing here is long-lived enough to flush its own window.
    """
    data = state()
    window = data.get("window") or {}
    hour = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H")
    if window.get("hour") != hour:
        if window.get("hour"):
            asked, throttled, idle = (window.get(k, 0) for k in ("no_capacity", "throttled", "idle"))
            print(f"{window['hour']}Z: {asked + throttled} asked, {asked} no capacity, "
                  f"{throttled} throttled, {idle} skipped while cooling")
        window = {"hour": hour}
    window[kind] = window.get(kind, 0) + 1
    data["window"] = window
    save_state(data)


def shapes_for_this_run():
    """One launch call per run, and never an idle run while a shape is available.

    The ARM is the machine worth having, so it gets every turn; the micro takes one turn in five,
    plus any turn the ARM is cooling down from. Only when both are cooling does the run do nothing.
    """
    data = state()
    runs = data.get("runs", 0) + 1
    data["runs"] = runs
    save_state(data)

    arm, micro = SHAPES[0], SHAPES[1]
    order = [micro, arm] if runs % MICRO_EVERY == 0 else [arm, micro]
    return [entry for entry in order if not cooling(entry["shape"])][:1]


def main():
    config = discover()

    already = existing()
    if already is None:
        sys.exit("cannot reach the OCI API")
    if already:
        instance = already[0]
        print(f"{NAME} already exists ({instance['lifecycleState']}), nothing to do")
        return

    candidates = shapes_for_this_run()
    if not candidates:
        tally("idle")
        return

    attempts = state().get("attempts", 0)
    for entry in candidates:
        instance, error = launch(config, entry)
        attempts += 1
        if instance:
            data = state()
            data["attempts"] = attempts
            data["created"] = instance["id"]
            save_state(data)
            ip = public_ip(instance["id"])
            waited = f" en el intento {attempts}" if attempts > 1 else ""
            telegram(f"🎉 <b>Oracle: instancia creada</b>{waited}\n\n"
                     f"Shape: <code>{entry['shape']}</code>\n"
                     f"IP publica: <code>{ip or 'aun asignandose'}</code>\n"
                     f"Entra con: <code>ssh -i ~/.ssh/homelab ubuntu@{ip or 'IP'}</code>")
            print(f"created with {entry['shape']}, ip {ip}")
            return
        low = (error or "").lower()
        if any(marker in low for marker in CAPACITY_MARKERS):
            # The call went through, which is what resets the throttle ladder.
            clear_throttles(entry["shape"])
            tally("no_capacity")
            continue
        if "429" in low or "too many requests" in low:
            start_backoff(entry["shape"])
            tally("throttled")
            break
        print(f"{entry['shape']}: {error}", file=sys.stderr)

    data = state()
    data["attempts"] = attempts
    data["last_try"] = email.utils.format_datetime(
        datetime.datetime.now(datetime.timezone.utc), usegmt=True)
    save_state(data)


if __name__ == "__main__":
    main()
