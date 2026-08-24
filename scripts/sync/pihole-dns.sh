#!/bin/bash
# Push every https://*.platanosverdes.com host declared in Caddy into Pi-hole's custom DNS.
#
# The hostname list is derived from the Caddy config rather than kept here, so the two cannot
# drift: this repo's Caddyfile, config/caddy/services/*.caddy, and rpi-services' fragment at
# EXT_CADDY_PATH when that repo is present.
#
# Additive only: a hostname is touched only if it was derived from Caddy, and only when its IP is
# stale. An entry added by hand for something else is never read as ours and never deleted.
set -uo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
set -a; source "$PROJECT_DIR/.env"; set +a

PIHOLE_BASE="http://localhost:8081/api"

if [[ -z "${TAILSCALE_IP:-}" || -z "${PIHOLE_TOKEN:-}" ]]; then
    echo "sync-pihole-dns: TAILSCALE_IP or PIHOLE_TOKEN not set in .env, skipping" >&2
    exit 0
fi

sources=("$PROJECT_DIR/config/caddy/Caddyfile" "$PROJECT_DIR"/config/caddy/services/*.caddy)
[[ -n "${EXT_CADDY_PATH:-}" ]] && sources+=("$EXT_CADDY_PATH"/*.caddy)

wanted=$(grep -ohP '^https://\K[\w.-]+\.platanosverdes\.com(?=\s*\{)' "${sources[@]}" 2>/dev/null | sort -u)
if [[ -z "$wanted" ]]; then
    echo "sync-pihole-dns: no *.platanosverdes.com hosts found in Caddy config, skipping" >&2
    exit 0
fi

sid=$(curl -sf -X POST "$PIHOLE_BASE/auth" \
    --data "$(jq -n --arg p "$PIHOLE_TOKEN" '{password:$p}')" \
    -H "Content-Type: application/json" | jq -r '.session.sid // empty')
if [[ -z "$sid" ]]; then
    echo "sync-pihole-dns: could not reach Pi-hole's API, skipping" >&2
    exit 0
fi

current=$(curl -sf "$PIHOLE_BASE/config/dns/hosts?sid=$sid" | jq -r '.config.dns.hosts[]? // empty')

added=0 updated=0 unchanged=0
while IFS= read -r host; do
    [[ -z "$host" ]] && continue
    existing_ip=$(printf '%s\n' "$current" | awk -v h="$host" '$2==h{print $1; exit}')

    if [[ "$existing_ip" == "$TAILSCALE_IP" ]]; then
        unchanged=$((unchanged + 1))
        continue
    fi

    if [[ -n "$existing_ip" ]]; then
        old_value=$(jq -rn --arg v "$existing_ip $host" '$v|@uri')
        curl -s -o /dev/null -X DELETE "$PIHOLE_BASE/config/dns/hosts/$old_value?sid=$sid"
        updated=$((updated + 1))
    else
        added=$((added + 1))
    fi

    new_value=$(jq -rn --arg v "$TAILSCALE_IP $host" '$v|@uri')
    status=$(curl -s -o /dev/null -w '%{http_code}' -X PUT "$PIHOLE_BASE/config/dns/hosts/$new_value?sid=$sid")
    if [[ "$status" != 2* ]]; then
        echo "sync-pihole-dns: failed to write $host (HTTP $status)" >&2
    fi
done <<< "$wanted"

left_alone=$(comm -23 \
    <(printf '%s\n' "$current" | awk 'NF{print $2}' | sort -u) \
    <(printf '%s\n' "$wanted" | sort -u) | grep -c . || true)
echo "pihole DNS: $added added, $updated re-pointed, $unchanged unchanged, $left_alone unrelated entries left untouched"
