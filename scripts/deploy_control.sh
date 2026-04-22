#!/bin/bash

# Project Directory
PROJECT_DIR="$HOME/rpi-homeserver"
LOG_FILE="$PROJECT_DIR/deploy.log"
PUSHGATEWAY_URL="http://localhost:9091"
DEPLOY_STATE_FILE="$PROJECT_DIR/.deploy_state"

# Load persistent counters
TOTAL_RUNS=0; DEPLOYS_WITH_CHANGES=0; DEPLOY_ERRORS=0
if [[ -f "$DEPLOY_STATE_FILE" ]]; then
    source "$DEPLOY_STATE_FILE" || true
fi
TOTAL_RUNS=$((TOTAL_RUNS + 1))

push_metrics() {
    local status=$1  # 0=no_change, 1=changed, 2=error
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

# Function for timestamped logging
log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') - $1"
}

# Navigate to project directory
cd "$PROJECT_DIR" || { echo "Directory not found"; exit 1; }

# 1. Sync with remote repository
log "Fetching updates from origin..."
BEFORE=$(git rev-parse HEAD)
if ! git pull origin main; then
    log "Error: Git pull failed."
    DEPLOY_ERRORS=$((DEPLOY_ERRORS + 1))
    push_metrics 2
    exit 1
fi
AFTER=$(git rev-parse HEAD)

# 2. Security Check: Ensure .env exists
if [ ! -f .env ]; then
    log "Error: .env file not found. Deployment aborted."
    DEPLOY_ERRORS=$((DEPLOY_ERRORS + 1))
    push_metrics 2
    exit 1
fi

# 3. Apply changes with Docker Compose
if [ "$BEFORE" != "$AFTER" ]; then
    log "Changes detected ($BEFORE -> $AFTER), rebuilding..."
    if ! sudo docker compose up -d --build --remove-orphans; then
        log "Error: Docker Compose failed to update."
        DEPLOY_ERRORS=$((DEPLOY_ERRORS + 1))
        push_metrics 2
        exit 1
    fi
    DEPLOYS_WITH_CHANGES=$((DEPLOYS_WITH_CHANGES + 1))
    DEPLOY_STATUS=1
else
    log "No changes detected, ensuring containers are running..."
    if ! sudo docker compose up -d --remove-orphans; then
        log "Error: Docker Compose failed."
        DEPLOY_ERRORS=$((DEPLOY_ERRORS + 1))
        push_metrics 2
        exit 1
    fi
    DEPLOY_STATUS=0
fi

# 4. Infrastructure Cleanup
log "Cleaning up unused Docker images..."
sudo docker image prune -f > /dev/null

log "Deployment completed successfully."
push_metrics $DEPLOY_STATUS

# Keep only the last 100 lines of the log file to save space
tail -n 100 "$LOG_FILE" > "${LOG_FILE}.tmp" && mv "${LOG_FILE}.tmp" "$LOG_FILE"

# crontab -e
# */15 * * * * /home/raspi/rpi-homeserver/deploy_control.sh >> /home/raspi/rpi-homeserver/deploy_control.log 2>&1