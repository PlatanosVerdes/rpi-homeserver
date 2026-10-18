#!/bin/bash
# Deploys this branch: fast-forwards to origin/streamhalla and applies compose. Cron, every 5 min:
#   */5 * * * * /home/raspberry/rpi-homeserver/scripts/deploy/apply.sh >> /home/raspberry/rpi-homeserver/apply.log 2>&1
# A commit pushed to the branch is live within 5 minutes, and it recreates whatever it changes,
# a live channel included.
set -uo pipefail
cd "$(dirname "$0")/../.."

exec 9>.deploy.lock
flock -n 9 || exit 0

log() { echo "$(date '+%F %T') - $*"; }

before=$(git rev-parse HEAD)
if ! git fetch -q origin streamhalla || ! git merge -q --ff-only origin/streamhalla; then
    log "Could not fast-forward to origin/streamhalla; applying what is checked out."
fi
after=$(git rev-parse HEAD)
[[ "$before" != "$after" ]] && log "Updated $(git log --oneline -1 | cut -c1-80)"

# Quiet when nothing changes; the output only matters when compose fails or recreates something.
if ! out=$(docker compose up -d --remove-orphans 2>&1); then
    log "docker compose failed:"
    echo "$out"
    exit 1
fi
grep -E "Recreated|Created|Removed" <<<"$out" | sed "s/^/$(date '+%F %T') - /"
exit 0
