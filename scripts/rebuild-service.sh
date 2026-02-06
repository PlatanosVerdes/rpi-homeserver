#!/bin/bash

# Rebuild a docker-compose service from scratch
# Usage: ./scripts/rebuild-service.sh <service-name>

set -e

if [ -z "$1" ]; then
    echo "Error: service name required"
    echo "Usage: ./scripts/rebuild-service.sh <service-name>"
    echo ""
    echo "Available services:"
    docker-compose config --services
    exit 1
fi

SERVICE=$1

echo "Rebuilding $SERVICE..."
echo ""

echo "Stopping container..."
docker-compose stop "$SERVICE" 2>/dev/null || true

echo "Removing container and image..."
docker-compose rm -f "$SERVICE" 2>/dev/null || true
docker rmi "$SERVICE" 2>/dev/null || true

echo "Building image..."
docker-compose build --pull --no-cache "$SERVICE"
sudo docker compose up -d --force-recreate pol-academy-offers-bot

echo "Starting service..."
docker-compose up -d "$SERVICE"

echo "Done. Showing logs..."
echo ""
docker-compose logs -f "$SERVICE"
