#!/bin/bash
set -e

echo "🔐 Configuring permissions..."

# Get PUID and PGID from current user
PUID=$(id -u)
PGID=$(id -g)

echo "User: $(whoami)"
echo "PUID: $PUID"
echo "PGID: $PGID"

# Apply permissions to data folders
if [ -d "/mnt/data" ]; then
    echo "Applying permissions to /mnt/data..."
    sudo chown -R $PUID:$PGID /mnt/data
    sudo chmod -R 755 /mnt/data
fi

# Apply permissions to configuration folders
if [ -d "config" ]; then
    echo "Applying permissions to config/..."
    sudo chown -R $PUID:$PGID config/
    sudo chmod -R 755 config/
    
    # Grafana needs specific user (UID 472)
    if [ -d "config/grafana" ]; then
        echo "Setting Grafana-specific permissions (UID 472)..."
        sudo chown -R 472:472 config/grafana
    fi
    
    # Prometheus needs specific user (UID 65534 - nobody)
    if [ -d "config/prometheus" ]; then
        echo "Setting Prometheus-specific permissions (UID 65534)..."
        sudo chown -R 65534:65534 config/prometheus
    fi
fi

echo "✅ Permissions configured successfully"