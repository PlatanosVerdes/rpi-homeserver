#!/bin/bash
# Install or update the Jellyfin app on the Samsung TV, with the build that matches this server.
# Run by hand, with someone at the TV: a bad build leaves the TV without Jellyfin.
#   scripts/tv-jellyfin-app.sh [tv-ip]
set -euo pipefail

TV_IP="${1:-${TV_IP:-192.168.0.10}}"
INSTALLER="ghcr.io/georift/install-jellyfin-tizen"

if ! timeout 3 bash -c "</dev/tcp/$TV_IP/26101" 2>/dev/null; then
    echo "The TV at $TV_IP does not answer on 26101: it is off, or developer mode is off."
    echo "On the TV: Apps, type 1 2 3 4 5, Developer mode On, Host PC IP = this machine, then restart the TV."
    exit 1
fi

version=$(curl -sf --max-time 5 http://localhost:8096/System/Info/Public | python3 -c 'import json,sys; print(json.load(sys.stdin)["Version"])')
major=${version%%.*}
minor=$(cut -d. -f2 <<<"$version")
# The default build follows the newest jellyfin-web and hangs on an older server.
if [[ "$major" == 10 ]]; then
    build="Jellyfin-10.$minor.z"
else
    build="Jellyfin-$major.z"
fi
echo "Jellyfin server $version, installing $build on $TV_IP"

# The installer image is amd64 only; the registration is lost on every reboot.
if [[ ! -e /proc/sys/fs/binfmt_misc/qemu-x86_64 ]]; then
    docker run --privileged --rm tonistiigi/binfmt --install amd64 >/dev/null
fi

out=$(docker run --rm --net host --platform linux/amd64 "$INSTALLER" "$TV_IP" "$build" 2>&1 | grep -v 'installing\[' || true)
echo "$out" | grep -vE '^\s*$|^ +[0-9]+K ' | tail -8
if grep -q 'download failed\[116\]' <<<"$out"; then
    echo
    echo "The TV refused to replace the installed app. Remove it on the TV (Apps, the gear icon, Jellyfin, Delete) and run this again."
    exit 1
fi
grep -q 'successfully installed' <<<"$out"
