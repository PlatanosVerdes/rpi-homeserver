#!/bin/bash

PROJECT_DIR="$HOME/rpi-homeserver"
SERVICES_DIR="$HOME/rpi-services"
PUSHGATEWAY_URL="http://localhost:9091"
DEPLOY_STATE_FILE="$PROJECT_DIR/.deploy_state"

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

# Cron used to truncate this log on every run; now cron and the webhook both append to it,
# so keep it bounded. Truncate in place (not mv) or the open redirect writes to a dead inode.
LOG_FILE="$PROJECT_DIR/deploy_control.log"
if [[ -f "$LOG_FILE" ]] && [[ "$(stat -c%s "$LOG_FILE" 2>/dev/null || echo 0)" -gt 5242880 ]]; then
    tail -n 500 "$LOG_FILE" > "$LOG_FILE.tmp" && cat "$LOG_FILE.tmp" > "$LOG_FILE"
    rm -f "$LOG_FILE.tmp"
fi

# Cron and the webhook receiver can both fire this; never let two deploys overlap.
exec 9>"$PROJECT_DIR/.deploy.lock"
if ! flock -n 9; then
    log "Another deploy is already running, skipping."
    exit 0
fi

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

    log "[$label] Pulling..."
    if ! git pull origin main 2>&1 | while IFS= read -r line; do log "[$label] $line"; done; then
        log "[$label] Git pull failed (repo may not be pushed yet), ensuring containers are running..."
        docker compose up -d --remove-orphans 2>/dev/null
        return 2
    fi
    after=$(git rev-parse HEAD 2>/dev/null || echo "none")

    # Must happen after the pull and before compose can restart Grafana
    [[ "$label" == "homeserver" ]] && render_grafana_alerting

    if [ "$before" != "$after" ]; then
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
bash "$PROJECT_DIR/scripts/install-crontab.sh" 2>&1 | while IFS= read -r line; do log "[cron] $line"; done

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
    sudo docker image prune -f > /dev/null
    touch "$PRUNE_MARKER"
fi

push_metrics $DEPLOY_STATUS
log "Done."

# crontab -e
# */15 * * * * /home/raspi/rpi-homeserver/scripts/deploy_control.sh >> /home/raspi/rpi-homeserver/deploy_control.log 2>&1
