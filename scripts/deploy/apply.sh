#!/bin/bash

PROJECT_DIR="$HOME/rpi-homeserver"
SERVICES_DIR="$HOME/rpi-services"
PUSHGATEWAY_URL="http://localhost:9091"
DEPLOY_STATE_FILE="$PROJECT_DIR/.deploy_state"
# The webhook receiver sets this to "webhook"; cron leaves the default
DEPLOY_TRIGGER="${DEPLOY_TRIGGER:-cron}"

set -a; source "$PROJECT_DIR/.env"; set +a

# APP_CONFIG_PATH may be relative (e.g. ./appdata); resolve it against the project dir
APPDATA="${APP_CONFIG_PATH:-./appdata}"
[[ "$APPDATA" != /* ]] && APPDATA="$PROJECT_DIR/${APPDATA#./}"

TOTAL_RUNS=0; DEPLOYS_WITH_CHANGES=0; DEPLOY_ERRORS=0
if [[ -f "$DEPLOY_STATE_FILE" ]]; then
    source "$DEPLOY_STATE_FILE" || true
fi
TOTAL_RUNS=$((TOTAL_RUNS + 1))

log() { echo "$(date '+%Y-%m-%d %H:%M:%S') - $1"; }

# Bounding apply.log is logrotate's job (config/logrotate/rpi-homeserver, installed below).
# This used to truncate to the last 500 lines at 5 MB, which kept the file small by throwing the
# history away; logrotate keeps four compressed rounds of it instead.

# Cron and the webhook receiver can both fire this; never let two deploys overlap.
#
# The skipped run used to be dropped, and that lost real deploys: a Go build on ARM holds the
# lock for minutes, and two pushes in that window (a tag in one repo, its version bump in the
# other) meant the second change waited for the next cron sweep half an hour later. So the one
# that cannot get in leaves a note, and whoever holds the lock takes one more pass before
# leaving. Coalescing, not a queue: ten pushes during a build still cost exactly one extra run.
PENDING_MARKER="$PROJECT_DIR/.deploy.pending"
LOCK_FILE="$PROJECT_DIR/.deploy.lock"
# A normal run takes about a minute and a Go build on ARM about five, so anything past this is
# not slow, it is stuck. It happened: a `docker exec` into an unresponsive container hung for
# an hour holding the lock, and every deploy after it was skipped -- the log said "already
# running" forty times and nobody deployed anything.
STUCK_AFTER=${DEPLOY_STUCK_AFTER:-2700}

# Append, not truncate: `9>` empties the file on open, so the one arriving would wipe the pid
# of the one working and then read it back empty. flock does not care which mode it is.
exec 9>>"$LOCK_FILE"
if ! flock -n 9; then
    holder=$(head -1 "$LOCK_FILE" 2>/dev/null || true)
    age=""
    [[ "$holder" =~ ^[0-9]+$ ]] && age=$(ps -o etimes= -p "$holder" 2>/dev/null | tr -d " ")
    if [[ -n "$age" ]] && (( age > STUCK_AFTER )); then
        log "Deploy $holder has been running ${age}s and is stuck; taking over."
        # Let go of our own descriptor first: the sweep below kills by open file, and that
        # would otherwise include us.
        exec 9>&-
        kill -TERM "$holder" 2>/dev/null || true
        sleep 5
        kill -KILL "$holder" 2>/dev/null || true
        # Killing the script is not enough. Its children inherited the lock descriptor, and
        # once the parent dies they are orphans, so no -P sweep finds them: the lock stays held
        # by a `sleep` or a hung `docker exec` with no visible owner. Kill by who holds the
        # file, which is the only thing that is actually true.
        fuser -k -KILL "$LOCK_FILE" 2>/dev/null || true
        sleep 1
        exec 9>>"$LOCK_FILE"
    fi
    if ! flock -n 9; then
        touch "$PENDING_MARKER"
        log "Another deploy is already running; left a note for it to run again."
        exit 0
    fi
fi
# Whoever holds the lock says so, so the next one can tell "busy" from "stuck". Written through
# its own descriptor to replace the previous holder's pid; truncating the file does not drop the
# lock, which lives on the open descriptor.
printf '%s\n' "$$" > "$LOCK_FILE"
# Claimed: anything asked for before now is about to be applied anyway.
rm -f "$PENDING_MARKER"

# RERUNS bounds the chain. Two extra passes absorb a burst of pushes; beyond that the cron
# sweep can have it, because something is pushing faster than the Pi can build.
RERUNS=${DEPLOY_RERUNS:-0}
# Kept in an array because inside a function "$@" would be the function's arguments, not the
# script's, and the re-run has to be the same invocation.
SCRIPT_ARGS=("$@")
rerun_if_pending() {
    if [[ -f "$PENDING_MARKER" ]] && (( RERUNS < 2 )); then
        rm -f "$PENDING_MARKER"
        log "A push landed while deploying; running once more."
        flock -u 9
        exec 9>&-
        DEPLOY_RERUNS=$((RERUNS + 1)) exec "$0" "${SCRIPT_ARGS[@]}"
    fi
}
trap rerun_if_pending EXIT

push_metrics() {
    local status=$1
    # Legacy job (keeps existing deploy dashboard working)
    cat <<EOF | curl -fsSL --connect-timeout 5 --data-binary @- "${PUSHGATEWAY_URL}/metrics/job/deploy_control" 2>/dev/null
# HELP deploy_run_total Total deploy script executions
# TYPE deploy_run_total counter
deploy_run_total $TOTAL_RUNS
# HELP deploy_with_changes_total Deploys that applied changes
# TYPE deploy_with_changes_total counter
deploy_with_changes_total $DEPLOYS_WITH_CHANGES
# HELP deploy_errors_total Failed deployments
# TYPE deploy_errors_total counter
deploy_errors_total $DEPLOY_ERRORS
# HELP deploy_last_run_timestamp Last execution timestamp
# TYPE deploy_last_run_timestamp gauge
deploy_last_run_timestamp $(date +%s)
# HELP deploy_last_status Last deploy status (0=no_change, 1=changed, 2=error)
# TYPE deploy_last_status gauge
deploy_last_status $status
# HELP deploy_last_trigger What ran the last deploy (webhook = push, cron = fallback sweep)
# TYPE deploy_last_trigger gauge
deploy_last_trigger{trigger="$DEPLOY_TRIGGER"} 1
EOF
    cat <<EOF > "$DEPLOY_STATE_FILE"
TOTAL_RUNS=$TOTAL_RUNS
DEPLOYS_WITH_CHANGES=$DEPLOYS_WITH_CHANGES
DEPLOY_ERRORS=$DEPLOY_ERRORS
EOF
}

push_repo_metrics() {
    local repo=$1 rc=$2 ts=$3
    # Normalize deploy_repo() return code (0=changed, 1=error, 2=no_change) to the
    # standard status code used everywhere else (0=no_change, 1=changed, 2=error).
    local status
    case "$rc" in
        0) status=1 ;;
        1) status=2 ;;
        2) status=0 ;;
        *) status=$rc ;;
    esac
    cat <<EOF | curl -fsSL --connect-timeout 5 --data-binary @- "${PUSHGATEWAY_URL}/metrics/job/deploy_repo/repo/${repo}" 2>/dev/null
# HELP deploy_repo_last_status Last deploy status per repo (0=no_change, 1=changed, 2=error)
# TYPE deploy_repo_last_status gauge
deploy_repo_last_status{repo="${repo}"} $status
# HELP deploy_repo_last_run_timestamp Last run timestamp per repo
# TYPE deploy_repo_last_run_timestamp gauge
deploy_repo_last_run_timestamp{repo="${repo}"} $ts
EOF
}

render_grafana_alerting() {
    # Grafana reads its alerting config from appdata (see compose-mon.yml), not straight from
    # git, because the Telegram token and chat id must not be committed. Rules and policies are
    # copied as-is; the contact point is rendered from its .tmpl with values from .env.
    local src="$PROJECT_DIR/config/grafana/alerting"
    local dst="$APPDATA/grafana-alerting"
    [[ -d "$src" ]] || return 0

    # sudo throughout: Docker creates bind-mount targets under appdata as root, so this
    # directory is root-owned whether or not the script got there first.
    sudo mkdir -p "$dst"

    # No bot configured: leave the directory empty. A policy pointing at a contact point that
    # does not exist fails provisioning, and that takes all of Grafana down.
    if [[ -z "${TELEGRAM_ALERT_BOT_TOKEN:-}" || -z "${TELEGRAM_ALERT_CHAT_ID:-}" ]]; then
        sudo rm -f "$dst"/*.yml
        log "Grafana alerting: no Telegram credentials in .env, alerting left unprovisioned"
        return 0
    fi

    sudo cp -f "$src"/policies.yml "$src"/rules.yml "$dst"/
    sed -e "s|\${TELEGRAM_ALERT_BOT_TOKEN}|${TELEGRAM_ALERT_BOT_TOKEN}|g" \
        -e "s|\${TELEGRAM_ALERT_CHAT_ID}|${TELEGRAM_ALERT_CHAT_ID}|g" \
        "$src/contact-points.yml.tmpl" | sudo tee "$dst/contact-points.yml" >/dev/null
    # holds the bot token; Grafana runs as root in its container and can still read it
    sudo chmod 600 "$dst/contact-points.yml"
}

# build_triggers is the alternation of paths that make a rebuild necessary, read from compose
# itself so neither repo needs a hand-kept list: a service's build context is where its image
# comes from. versions.env and the compose files are in it unconditionally — the first pins image
# tags and even a remote build context, the second can move a build section. Remote contexts
# (rpi-services builds every app from a pinned git URL) are left out on purpose: a local diff
# cannot change them, versions.env is what moves those.
build_triggers() {
    local config paths triggers
    config=$(docker compose config --format json 2>/dev/null)
    if [[ -z "$config" ]]; then
        # Unreadable config is the one case worth rebuilding blindly: a rebuild that was not
        # needed costs a restart, one that was skipped ships stale code.
        log "[$label] WARNING: cannot read the compose config, rebuilding to be safe"
        echo '.*'
        return
    fi
    paths=$(printf '%s' "$config" | python3 -c '
import json, os, sys
try:
    config = json.load(sys.stdin)
except Exception:
    sys.exit(0)
root = os.getcwd()
for service in (config.get("services") or {}).values():
    context = (service.get("build") or {}).get("context")
    if not context or "://" in context:
        continue
    relative = os.path.relpath(context, root)
    if not relative.startswith(".."):
        print(relative.rstrip("/") + "/")
' | sort -u | tr '\n' '|')
    triggers='versions\.env|docker-compose\.yml|compose-[^/]*\.yml'
    # Never leave a trailing separator: an empty alternative matches every line, which would
    # silently turn this back into rebuilding always.
    [[ -n "${paths%|}" ]] && triggers="${triggers}|${paths%|}"
    echo "$triggers"
}

deploy_repo() {
    local dir=$1
    local label=$2

    cd "$dir" || { log "[$label] Directory not found, skipping."; return 1; }

    if [ ! -f .env ]; then
        log "[$label] .env not found, skipping."
        return 1
    fi

    # Load image versions from versions.env alongside .env (see versions.env)
    if [ -f versions.env ]; then
        export COMPOSE_ENV_FILES="versions.env,.env"
    else
        export COMPOSE_ENV_FILES=".env"
    fi

    local before after
    before=$(git rev-parse HEAD 2>/dev/null || echo "none")

    # --no-rebase, not bare `git pull`. With no strategy configured git refuses outright the
    # moment the branches diverge ("Need to specify how to reconcile"), and a commit made on the
    # Pi while another was pushed from a laptop is enough to do it. That aborts the pull, so the
    # deploy silently stops applying anything until someone reconciles by hand. Merging is right
    # here and rebasing is not: local commits may be someone else's work in flight, and rewriting
    # their SHAs underneath them is worse than a merge commit.
    log "[$label] Pulling..."
    if ! git pull --no-rebase origin main 2>&1 | while IFS= read -r line; do log "[$label] $line"; done; then
        log "[$label] Git pull failed (repo may not be pushed yet), ensuring containers are running..."
        docker compose up -d --remove-orphans 2>/dev/null
        return 2
    fi
    after=$(git rev-parse HEAD 2>/dev/null || echo "none")

    # Merging on its own would hide the real risk: the Pi keeps deploying happily while commits
    # made here exist nowhere else. Say so, because an SD card is the one copy that dies.
    local unpushed
    unpushed=$(git rev-list --count origin/main..HEAD 2>/dev/null || echo 0)
    if [[ "$unpushed" -gt 0 ]]; then
        log "[$label] WARNING: $unpushed commit(s) exist only on this Pi, push them somewhere safe"
    fi

    # Must happen after the pull and before compose can restart Grafana
    [[ "$label" == "homeserver" ]] && render_grafana_alerting

    # qbit_manage rewrites its own config on every run, adding whatever defaults its version wants,
    # so it cannot be handed the git copy: the working tree would be permanently dirty and the next
    # pull would abort with "local changes would be overwritten". The committed file is the source
    # and this is the working copy it is free to scribble on.
    # Docker creates a missing bind-mount target as root, which locks out every container that runs
    # as PUID:PGID: autobrr crash-looped on "permission denied" creating its own database, and
    # qbit-manage before it could not be handed its config. So pre-create those directories, and
    # only those: appdata holds ten directories that are legitimately root-owned because their
    # container runs as root, and chowning those would break them instead.
    #
    # The list comes from the resolved compose config rather than a hand-kept one here, so a new
    # service is covered the day it is added and nobody has to remember this comment exists.
    if [[ "$label" == "homeserver" ]]; then
        local appdata_dirs
        appdata_dirs=$(cd "$PROJECT_DIR" && docker compose --profile all config --format json 2>/dev/null |
            python3 -c '
import json, sys
try:
    doc = json.load(sys.stdin)
except ValueError:
    sys.exit(0)
for svc in (doc.get("services") or {}).values():
    if svc.get("user") != "1000:1000":
        continue
    for vol in svc.get("volumes") or []:
        src = vol.get("source") or ""
        if "/appdata/" in src:
            print(src)
' | sort -u) || appdata_dirs=""
        while IFS= read -r dir; do
            [[ -n "$dir" ]] || continue
            if [[ ! -d "$dir" ]] || [[ "$(stat -c %u "$dir")" != "$(id -u)" ]]; then
                sudo mkdir -p "$dir" && sudo chown "$(id -u):$(id -g)" "$dir" &&
                    log "[$label] fixed ownership of ${dir#"$PROJECT_DIR/"}"
            fi
        done <<< "$appdata_dirs"
    fi

    # qbit_manage rewrites its own config on every run, adding whatever defaults its version wants,
    # so it cannot be handed the git copy: the working tree would be permanently dirty and the next
    # pull would abort with "local changes would be overwritten". The committed file is the source
    # and this is the working copy it is free to scribble on.
    if [[ "$label" == "homeserver" && -f "$PROJECT_DIR/config/qbit-manage/config.yml" ]]; then
        if ! cmp -s "$PROJECT_DIR/config/qbit-manage/config.yml" \
            "$PROJECT_DIR/appdata/qbit-manage/config.yml"; then
            cp "$PROJECT_DIR/config/qbit-manage/config.yml" \
                "$PROJECT_DIR/appdata/qbit-manage/config.yml"
            log "[$label] qbit-manage config updated from the committed copy"
        fi
    fi

    # The webhook receiver is a host systemd service, so a pull updates its files while the old
    # process keeps serving. Apply them here or the change is silently ignored. Safe to restart
    # from inside a deploy this same service may have spawned, thanks to KillMode=process.
    if [[ "$label" == "homeserver" && "$before" != "$after" ]] &&
        git diff --name-only "$before" "$after" | grep -q '^services/deploy-webhook/'; then
        local unit=/etc/systemd/system/deploy-webhook.service
        if ! sudo cmp -s services/deploy-webhook/deploy-webhook.service "$unit"; then
            if sudo cp services/deploy-webhook/deploy-webhook.service "$unit" &&
                sudo systemctl daemon-reload; then
                log "[$label] webhook unit updated"
            else
                log "[$label] WARNING: could not install the new webhook unit"
            fi
        fi
        if sudo systemctl restart deploy-webhook; then
            log "[$label] webhook receiver restarted with the new code"
        else
            log "[$label] WARNING: could not restart deploy-webhook, it is still running the old code"
        fi
    fi

    # Grafana reads alerting provisioning ONLY at startup. Dashboards reload on their own (their
    # file provider polls), alert rules do not, so a committed rule would sit there doing nothing.
    if [[ "$label" == "homeserver" && "$before" != "$after" ]] &&
        git diff --name-only "$before" "$after" | grep -q '^config/grafana/alerting/' &&
        docker ps --format '{{.Names}}' | grep -qx grafana; then
        if docker restart grafana >/dev/null 2>&1; then
            log "[$label] alerting rules changed, Grafana restarted to load them"
        else
            log "[$label] WARNING: alerting changed but Grafana would not restart"
        fi
    fi

    # Same trap as the alerting config above: the Caddyfile is a bind mount, so changing it never
    # makes compose recreate the container, and Caddy only reads it at startup. Every routing change
    # committed here has silently needed a manual restart to take effect.
    # `caddy reload` and not `docker restart`: it swaps the config in place without dropping
    # connections or re-reading the certificate store.
    if [[ "$label" == "homeserver" && "$before" != "$after" ]] &&
        git diff --name-only "$before" "$after" | grep -q '^config/caddy/' &&
        docker ps --format '{{.Names}}' | grep -qx caddy; then
        if timeout 60 docker exec caddy caddy reload --config /etc/caddy/Caddyfile >/dev/null 2>&1; then
            log "[$label] Caddy config changed, reloaded"
        else
            log "[$label] WARNING: Caddy config changed but the reload was rejected, still on the old routes"
        fi
    fi

    # Third instance of the same trap, and the most misleading one. Homepage watches services.yaml
    # and bookmarks.yaml and picks those up live, so config changes appear to apply — but
    # settings.yaml is read once at startup, and that is where `layout:` lives, which is what
    # actually orders the groups on the page. Reordering therefore looked like it did nothing.
    if [[ "$label" == "homeserver" && "$before" != "$after" ]] &&
        git diff --name-only "$before" "$after" | grep -qE '^config/homepage/settings\.ya?ml$' &&
        docker ps --format '{{.Names}}' | grep -qx homepage; then
        if docker restart homepage >/dev/null 2>&1; then
            log "[$label] homepage settings changed, restarted to load them"
        else
            log "[$label] WARNING: homepage settings changed but it would not restart"
        fi
    fi

    # Fourth of the same, and the one that hid best. Vector reads its config once at startup, and
    # the file is a bind mount, so compose never recreates the container for it. Worse, git replaces
    # the file rather than editing it, so the running container keeps the old inode: host and
    # container had different checksums for the same path. A plain restart re-resolves the mount.
    if [[ "$label" == "homeserver" && "$before" != "$after" ]] &&
        git diff --name-only "$before" "$after" | grep -q '^config/vector/' &&
        docker ps --format '{{.Names}}' | grep -qx vector; then
        if docker restart vector >/dev/null 2>&1; then
            log "[$label] vector config changed, restarted to load it"
        else
            log "[$label] WARNING: vector config changed but it would not restart"
        fi
    fi

    if [ "$before" != "$after" ]; then
        # `--build` recreates every locally built container whether or not anything it is built
        # from changed, and a recreate cuts what that container was serving: caddy drops every
        # live HTTP connection, acestream-proxy kills the stream Jellyfin is reading. On
        # 2026-08-24 a diff of Grafana rules, tracker config, docs and the crontab recreated
        # seven containers mid-match with the images provably unchanged. So build only when the
        # diff touches something an image actually comes from.
        local changed rebuild_when
        changed=$(git diff --name-only "$before" "$after")
        rebuild_when=$(build_triggers)
        if [[ -n "$changed" ]] && ! grep -qE "^(${rebuild_when})" <<<"$changed"; then
            log "[$label] Nothing an image is built from changed, skipping rebuild..."
            docker compose up -d --remove-orphans 2>/dev/null
            return 2  # no-op, nothing to rebuild
        fi

        log "[$label] Changes detected, rebuilding..."
        if ! docker compose up -d --build --remove-orphans 2>&1 | while IFS= read -r line; do log "[$label] $line"; done; then
            log "[$label] Docker Compose failed."
            return 1
        fi
        return 0  # changed
    else
        log "[$label] No changes, ensuring containers are running..."
        docker compose up -d --remove-orphans 2>/dev/null
        return 2  # no change
    fi
}

TS=$(date +%s)

# --- rpi-homeserver ---
deploy_repo "$PROJECT_DIR" "homeserver"
RESULT_HOME=$?
push_repo_metrics "homeserver" $RESULT_HOME $TS

# --- rpi-services (optional, skipped if not present) ---
RESULT_SERVICES=2
if [ -d "$SERVICES_DIR" ]; then
    deploy_repo "$SERVICES_DIR" "services"
    RESULT_SERVICES=$?
    push_repo_metrics "services" $RESULT_SERVICES $TS
fi

# Keep the host crontab in sync with both repos' fragments
bash "$PROJECT_DIR/scripts/deploy/install-crontab.sh" 2>&1 | while IFS= read -r line; do log "[cron] $line"; done

# And the logrotate policy that keeps the logs those cron jobs write from filling the disk
bash "$PROJECT_DIR/scripts/deploy/install-logrotate.sh" 2>&1 | while IFS= read -r line; do log "[logrotate] $line"; done

# Converge Radarr/Sonarr custom formats and quality profiles to config/arr/. These are built by
# hand through each app's own UI and live only in its appdata database, so without this a lost
# Pi silently loses them. Needs both apps up, hence running here rather than before compose.
bash "$PROJECT_DIR/scripts/sync/arr-config.sh" 2>&1 | while IFS= read -r line; do log "[arr-config] $line"; done

# The other direction, for the two apps whose settings are logic rather than credentials: autobrr's
# filters and Maintainerr's rule. Both are written by hand in a UI and held in a database, so git
# only has them because they were exported. This reports drift rather than pushing, because a wrong
# Maintainerr rule deletes films. See scripts/ops/config-export.py.
"$PROJECT_DIR/scripts/ops/config-export.py" --check 2>&1 | while IFS= read -r line; do log "[config-drift] $line"; done

# Converge Pi-hole's custom DNS to the *.platanosverdes.com hosts declared in Caddy. Additive
# only (see the script), so it never touches an entry it did not derive from Caddy.
bash "$PROJECT_DIR/scripts/sync/pihole-dns.sh" 2>&1 | while IFS= read -r line; do log "[pihole-dns] $line"; done

# Push Radarr/Sonarr's connection into Overseerr and Bazarr (their own link to each other, built
# by hand through each app's UI and otherwise lost with appdata).
bash "$PROJECT_DIR/scripts/sync/arr-links.sh" 2>&1 | while IFS= read -r line; do log "[arr-links] $line"; done

# Push PLEX_LAN_NETWORKS into Plex, so tailnet clients count as local and are not charged the
# Remote Watch Pass. No Docker image exposes this preference, and it only exists in appdata.
bash "$PROJECT_DIR/scripts/sync/plex-prefs.sh" 2>&1 | while IFS= read -r line; do log "[plex-prefs] $line"; done

# Converge qBittorrent's rate cap and queue limits to config/qbittorrent/preferences.json. Same
# reason as the others: they live only in appdata and an unlimited qBittorrent will flatten this Pi.
bash "$PROJECT_DIR/scripts/sync/qbit-config.sh" 2>&1 | while IFS= read -r line; do log "[qbit-config] $line"; done

# Aggregate status: error(1)>changed(0)>no-change(2)
if [ $RESULT_HOME -eq 1 ] || [ $RESULT_SERVICES -eq 1 ]; then
    DEPLOY_ERRORS=$((DEPLOY_ERRORS + 1))
    DEPLOY_STATUS=2
elif [ $RESULT_HOME -eq 0 ] || [ $RESULT_SERVICES -eq 0 ]; then
    DEPLOYS_WITH_CHANGES=$((DEPLOYS_WITH_CHANGES + 1))
    DEPLOY_STATUS=1
else
    DEPLOY_STATUS=0
fi

# Prune unused images at most once per day (avoids SD-card wear on every 15-min run)
PRUNE_MARKER="$PROJECT_DIR/.last_prune"
if [[ ! -f "$PRUNE_MARKER" ]] || find "$PRUNE_MARKER" -mmin +1380 -print 2>/dev/null | grep -q .; then
    log "Cleaning up unused Docker images (daily)..."
    # No sudo: the deploy user is in the docker group, and one less sudo is one less thing
    # that breaks depending on who invoked the script
    docker image prune -f > /dev/null
    touch "$PRUNE_MARKER"
fi

push_metrics $DEPLOY_STATUS
log "Done."
# The trap fires here: if a push landed while this was building, apply it now rather than
# leaving it for the cron sweep.

# crontab -e
# */15 * * * * /home/raspi/rpi-homeserver/scripts/deploy/apply.sh >> /home/raspi/rpi-homeserver/apply.log 2>&1
