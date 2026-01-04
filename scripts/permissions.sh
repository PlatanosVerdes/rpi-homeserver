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
fi

echo "✅ Permissions configured successfully"