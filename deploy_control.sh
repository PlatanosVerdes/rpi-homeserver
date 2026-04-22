#!/bin/bash

# Project Directory
PROJECT_DIR="$HOME/rpi-homeserver"
LOG_FILE="$PROJECT_DIR/deploy.log"

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
    exit 1
fi
AFTER=$(git rev-parse HEAD)

# 2. Security Check: Ensure .env exists
if [ ! -f .env ]; then
    log "Error: .env file not found. Deployment aborted."
    exit 1
fi

# 3. Apply changes with Docker Compose
if [ "$BEFORE" != "$AFTER" ]; then
    log "Changes detected ($BEFORE -> $AFTER), rebuilding..."
    if ! sudo docker compose up -d --build --remove-orphans; then
        log "Error: Docker Compose failed to update."
        exit 1
    fi
else
    log "No changes detected, ensuring containers are running..."
    if ! sudo docker compose up -d --remove-orphans; then
        log "Error: Docker Compose failed."
        exit 1
    fi
fi

# 4. Infrastructure Cleanup
log "Cleaning up unused Docker images..."
sudo docker image prune -f > /dev/null

log "Deployment completed successfully."

# Keep only the last 100 lines of the log file to save space
tail -n 100 "$LOG_FILE" > "${LOG_FILE}.tmp" && mv "${LOG_FILE}.tmp" "$LOG_FILE"

# crontab -e
# */15 * * * * /home/raspi/rpi-homeserver/deploy_control.sh >> /home/raspi/rpi-homeserver/deploy_control.log 2>&1