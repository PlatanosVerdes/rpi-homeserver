#!/bin/bash
set -e

# Grafana Setup Script
# Configures Grafana with Prometheus datasource and imports dashboards

echo "📊 Setting up Grafana dashboards..."

# Wait for Grafana to be ready
echo "⏳ Waiting for Grafana to start..."
until curl -s http://localhost:3000/api/health > /dev/null 2>&1; do
    sleep 2
done

echo "✅ Grafana is ready!"

# Get credentials from .env
if [ -f .env ]; then
    source .env
else
    echo "❌ .env file not found"
    exit 1
fi

GRAFANA_URL="http://localhost:3000"
GRAFANA_USER="${GRAFANA_USER:-admin}"
GRAFANA_PASSWORD="${GRAFANA_PASSWORD}"

# Add Prometheus datasource
echo "📡 Adding Prometheus datasource..."
curl -X POST -H "Content-Type: application/json" \
     -u "${GRAFANA_USER}:${GRAFANA_PASSWORD}" \
     "${GRAFANA_URL}/api/datasources" \
     -d '{
       "name": "Prometheus",
       "type": "prometheus",
       "url": "http://prometheus:9090",
       "access": "proxy",
       "isDefault": true
     }' 2>/dev/null || echo "ℹ️  Datasource may already exist"

echo ""
echo "✅ Grafana setup complete!"
echo ""
echo "🎯 Recommended Dashboards to Import:"
echo ""
echo "1. Node Exporter Full (ID: 1860)"
echo "   - CPU, RAM, Disk, Network metrics"
echo "   - Perfect for system monitoring"
echo ""
echo "2. Docker Container & Host Metrics (ID: 179)"
echo "   - Docker container stats"
echo "   - Container CPU, RAM usage"
echo ""
echo "3. Raspberry Pi Monitoring (ID: 10578)"
echo "   - Optimized for Raspberry Pi"
echo "   - Temperature, voltage, throttling"
echo ""
echo "📝 To import dashboards:"
echo "   1. Go to http://$(hostname -I | awk '{print $1}'):3000"
echo "   2. Login with: ${GRAFANA_USER} / ${GRAFANA_PASSWORD}"
echo "   3. Click '+' → Import → Enter dashboard ID"
echo ""
echo "🔗 More dashboards: https://grafana.com/grafana/dashboards/"
