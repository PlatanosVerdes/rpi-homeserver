#!/bin/bash
set -e

# Health Check Monitor Script
# Monitors all Docker containers health status

echo "🏥 Docker Container Health Status"
echo "=================================="
echo ""

# Get all containers with health status
containers=$(docker ps --format "table {{.Names}}\t{{.Status}}" | tail -n +2)

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

healthy=0
unhealthy=0
no_healthcheck=0

while IFS=$'\t' read -r name status; do
    if [[ $status == *"(healthy)"* ]]; then
        echo -e "${GREEN}✅ $name${NC} - Healthy"
        ((healthy++))
    elif [[ $status == *"(unhealthy)"* ]]; then
        echo -e "${RED}❌ $name${NC} - UNHEALTHY"
        ((unhealthy++))
    elif [[ $status == *"(health: starting)"* ]]; then
        echo -e "${YELLOW}⏳ $name${NC} - Health check starting..."
        ((no_healthcheck++))
    else
        echo -e "ℹ️  $name - No health check configured"
        ((no_healthcheck++))
    fi
done <<< "$containers"

echo ""
echo "=================================="
echo "Summary:"
echo -e "${GREEN}Healthy: $healthy${NC}"
echo -e "${RED}Unhealthy: $unhealthy${NC}"
echo -e "No health check: $no_healthcheck"

if [ $unhealthy -gt 0 ]; then
    echo ""
    echo "⚠️  Some containers are unhealthy!"
    echo "Check logs with: docker logs <container-name>"
    exit 1
fi

echo ""
echo "✅ All containers are healthy!"
exit 0
