#!/bin/bash
# Push every https://*.platanosverdes.com host declared in Caddy into Pi-hole's custom DNS.
#
# The hostname list is derived from the Caddy config rather than kept here, so the two cannot
# drift: this repo's Caddyfile, config/caddy/services/*.caddy, and rpi-services' fragment at
# EXT_CADDY_PATH when that repo is present.
#
# Ours is a *.platanosverdes.com entry pointing at TAILSCALE_IP: those follow Caddy, written when a
# route appears, re-pointed when stale, deleted when the route is gone. Anything else (another IP,
# another domain) was added by hand and is never touched.
#
# PIHOLE_DNS_PUBLIC_HOSTS (space separated) are hosts that must resolve to their public record
# even on the tailnet, e.g. one behind Cloudflare Access that signs you in there: they are not
# written, and our own entry for them (pointing at TAILSCALE_IP) is removed.
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
# A companion repo that is configured but has no routes on disk (not cloned yet, moved) would read
# as all its hosts gone, so pruning only runs when every configured source is there.
prune=1
if [[ -n "${EXT_CADDY_PATH:-}" ]] && ! compgen -G "$EXT_CADDY_PATH/*.caddy" >/dev/null; then
    echo "sync-pihole-dns: no routes found in EXT_CADDY_PATH ($EXT_CADDY_PATH), not removing anything" >&2
    prune=0
fi

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

public=$(tr ' ' '\n' <<< "${PIHOLE_DNS_PUBLIC_HOSTS:-}" | awk 'NF' | sort -u)
removed=0
while IFS= read -r host; do
    [[ -z "$host" ]] && continue
    if printf '%s\n' "$current" | awk -v h="$host" -v ip="$TAILSCALE_IP" '$1==ip && $2==h{f=1} END{exit !f}'; then
        value=$(jq -rn --arg v "$TAILSCALE_IP $host" '$v|@uri')
        curl -s -o /dev/null -X DELETE "$PIHOLE_BASE/config/dns/hosts/$value?sid=$sid"
        removed=$((removed + 1))
    fi
done <<< "$public"
[[ -n "$public" ]] && wanted=$(comm -23 <(printf '%s\n' "$wanted") <(printf '%s\n' "$public"))

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

# Each http://<name> site in Caddy is also <name>.pi at STATIC_IP: at home a browser reaches it on
# the LAN by name, without going through Tailscale, which the *.platanosverdes.com names do.
lan_wanted=$(grep -ohP '^http://\K[a-z0-9-]+(?=[\s,{])' "${sources[@]}" 2>/dev/null | sort -u | sed 's/$/.pi/')
lan=0
if [[ -n "${STATIC_IP:-}" ]]; then
    while IFS= read -r host; do
        [[ -z "$host" ]] && continue
        existing_ip=$(printf '%s\n' "$current" | awk -v h="$host" '$2==h{print $1; exit}')
        [[ "$existing_ip" == "$STATIC_IP" ]] && continue
        if [[ -n "$existing_ip" ]]; then
            old_value=$(jq -rn --arg v "$existing_ip $host" '$v|@uri')
            curl -s -o /dev/null -X DELETE "$PIHOLE_BASE/config/dns/hosts/$old_value?sid=$sid"
        fi
        new_value=$(jq -rn --arg v "$STATIC_IP $host" '$v|@uri')
        status=$(curl -s -o /dev/null -w '%{http_code}' -X PUT "$PIHOLE_BASE/config/dns/hosts/$new_value?sid=$sid")
        if [[ "$status" == 2* ]]; then
            lan=$((lan + 1))
        else
            echo "sync-pihole-dns: failed to write $host (HTTP $status)" >&2
        fi
    done <<< "$lan_wanted"
fi

pruned=0
if (( prune )); then
    stale=$(printf '%s\n' "$current" |
        awk -v ip="$TAILSCALE_IP" '$1==ip && $2 ~ /\.platanosverdes\.com$/ {print $2}' | sort -u |
        comm -23 - <(printf '%s\n' "$wanted" "$public" | awk 'NF' | sort -u))
    while IFS= read -r host; do
        [[ -z "$host" ]] && continue
        value=$(jq -rn --arg v "$TAILSCALE_IP $host" '$v|@uri')
        curl -s -o /dev/null -X DELETE "$PIHOLE_BASE/config/dns/hosts/$value?sid=$sid"
        pruned=$((pruned + 1))
    done <<< "$stale"
fi

left_alone=$(comm -23 \
    <(printf '%s\n' "$current" | awk 'NF{print $2}' | sort -u) \
    <(printf '%s\n' "$wanted" "$lan_wanted" | awk 'NF' | sort -u) | grep -c . || true)
echo "pihole DNS: $added added, $updated re-pointed, $unchanged unchanged, $pruned removed with their route, $removed left to public DNS, $lan LAN names written, $left_alone unrelated entries left untouched"
