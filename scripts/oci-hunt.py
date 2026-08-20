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

# And it rate-limits the asking, not just the capacity: too many launch attempts return 429 for the
# whole user. That is the real budget here, not the cron interval: a throttled run is a wasted turn,
# so asking twice as often can win fewer windows. Measured on the first round, one launch call went
# through ("out of host capacity") and the second in the same run got the 429, which is where these
# numbers come from: one call per run, the ARM shape every run because it is the one worth having,
# and the micro only every Nth run as a fallback.
MICRO_EVERY = 5

# Backoff is per shape, not global. Oracle words the 429 as "for the user", but the evidence says
# it is a bucket that refills in seconds rather than a punishment: in one round the first launch
# call went through and only the second was refused. So a shape that got throttled steps aside for
# a couple of minutes while the other one keeps asking, instead of both sitting out. It doubles
# while the refusals repeat and resets the moment a call goes through, so if the limit really is
# user-wide the ladder finds that out by itself.
BACKOFF_MINUTES = 2
BACKOFF_MAX = 30


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
        print("las dos formas en espera tras un 429, esta pasada no pregunta")
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
            print(f"{entry['shape']}: sin capacidad")
            continue
        if "429" in low or "too many requests" in low:
            minutes = start_backoff(entry["shape"])
            print(f"{entry['shape']}: 429, esperando {minutes} min")
            break
        print(f"{entry['shape']}: {error}", file=sys.stderr)

    data = state()
    data["attempts"] = attempts
    data["last_try"] = email.utils.format_datetime(
        datetime.datetime.now(datetime.timezone.utc), usegmt=True)
    save_state(data)


if __name__ == "__main__":
    main()
