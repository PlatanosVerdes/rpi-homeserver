#!/bin/bash
set -e

# DNS Setup Script for Pi-hole
# Configures local DNS entries for easy access to services

# Load configuration from .env
if [ -f .env ]; then
    source .env
else
    echo "❌ .env file not found!"
    exit 1
fi

LOCAL_DOMAIN="${DOMAIN:-banana.lan}"
HOSTNAME="${HOSTNAME:-banana}"

echo "🍌 Setting up local DNS with Pi-hole..."
echo ""

# Get local IP address
LOCAL_IP=$(hostname -I | awk '{print $1}')

if [ -z "$LOCAL_IP" ]; then
    echo "❌ Could not detect local IP address"
    exit 1
fi

echo "✅ Detected local IP: $LOCAL_IP"
echo "📝 Setting up DNS records for: $LOCAL_DOMAIN"
echo ""

# Create custom DNS file for Pi-hole
CUSTOM_DNS_FILE="./config/pihole/etc-dnsmasq/02-custom-dns.conf"

# Create directory if it doesn't exist
mkdir -p ./config/pihole/etc-dnsmasq

# Write DNS configuration
cat > "$CUSTOM_DNS_FILE" << EOF
# Custom DNS entries for $LOCAL_DOMAIN
# Generated automatically by setup_dns.sh

# Main hostname
address=/$HOSTNAME.local/$LOCAL_IP
address=/$HOSTNAME/$LOCAL_IP

# Service subdomains
address=/plex.$LOCAL_DOMAIN/$LOCAL_IP
address=/pihole.$LOCAL_DOMAIN/$LOCAL_IP
address=/nextcloud.$LOCAL_DOMAIN/$LOCAL_IP
address=/qbittorrent.$LOCAL_DOMAIN/$LOCAL_IP
address=/radarr.$LOCAL_DOMAIN/$LOCAL_IP
address=/sonarr.$LOCAL_DOMAIN/$LOCAL_IP
address=/prowlarr.$LOCAL_DOMAIN/$LOCAL_IP
address=/grafana.$LOCAL_DOMAIN/$LOCAL_IP
address=/prometheus.$LOCAL_DOMAIN/$LOCAL_IP
address=/tautulli.$LOCAL_DOMAIN/$LOCAL_IP

# Alternative: root domain
address=/$LOCAL_DOMAIN/$LOCAL_IP
EOF

echo "✅ DNS configuration created at: $CUSTOM_DNS_FILE"
echo ""
echo "📋 Configured DNS entries:"
echo "   - $HOSTNAME.local → $LOCAL_IP"
echo "   - $HOSTNAME → $LOCAL_IP"
echo "   - $LOCAL_DOMAIN → $LOCAL_IP"
echo "   - plex.$LOCAL_DOMAIN → $LOCAL_IP"
echo "   - pihole.$LOCAL_DOMAIN → $LOCAL_IP"
echo "   - nextcloud.$LOCAL_DOMAIN → $LOCAL_IP"
echo "   - qbittorrent.$LOCAL_DOMAIN → $LOCAL_IP"
echo "   - radarr.$LOCAL_DOMAIN → $LOCAL_IP"
echo "   - sonarr.$LOCAL_DOMAIN → $LOCAL_IP"
echo "   - prowlarr.$LOCAL_DOMAIN → $LOCAL_IP"
echo "   - grafana.$LOCAL_DOMAIN → $LOCAL_IP"
echo "   - prometheus.$LOCAL_DOMAIN → $LOCAL_IP"
echo "   - tautulli.$LOCAL_DOMAIN → $LOCAL_IP"
echo ""
echo "⚠️  Restart Pi-hole for changes to take effect:"
echo "   docker compose restart pihole"
echo ""
echo "💡 Access your services (short URLs!):"
echo "   - Plex: http://plex.$LOCAL_DOMAIN:32400/web"
echo "   - Pi-hole: http://pihole.$LOCAL_DOMAIN/admin"
echo "   - Grafana: http://grafana.$LOCAL_DOMAIN:3000"
echo "   - Nextcloud: https://nextcloud.$LOCAL_DOMAIN:444"
echo "   - qBittorrent: http://qbittorrent.$LOCAL_DOMAIN:8080"
echo ""
echo "🔧 Make sure your devices are using Pi-hole as DNS server!"
echo "   Configure your router to use $LOCAL_IP as primary DNS"
