#!/bin/bash

# Rebuild a docker compose service from scratch
# Usage: ./scripts/rebuild-service.sh <service-name>

set -e

# Load image versions alongside secrets (see versions.env)
export COMPOSE_ENV_FILES=versions.env,.env

if [ -z "$1" ]; then
    echo "Error: service name required"
    echo "Usage: ./scripts/rebuild-service.sh <service-name>"
    echo ""
    echo "Available services:"
    docker compose config --services
    exit 1
fi

SERVICE=$1

echo "Rebuilding $SERVICE..."
echo ""

echo "Stopping container..."
docker compose stop "$SERVICE" 2>/dev/null || true

echo "Removing container..."
docker compose rm -f "$SERVICE" 2>/dev/null || true

echo "Building and starting service..."
docker compose build --pull --no-cache "$SERVICE"
docker compose up -d --force-recreate "$SERVICE"

echo "Done. Showing logs..."
echo ""
docker compose logs -f "$SERVICE"
