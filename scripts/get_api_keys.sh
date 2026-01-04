#!/bin/bash
set -e

# Script to extract API keys from *arr apps

echo "🔑 Extracting API Keys from *arr applications..."
echo ""
echo "This script will help you get the API keys needed for monitoring."
echo ""

# Function to extract API key from config.xml
extract_api_key() {
    local app_name=$1
    local config_file=$2
    
    if [ -f "$config_file" ]; then
        api_key=$(grep -oP '<ApiKey>\K[^<]+' "$config_file" 2>/dev/null || echo "")
        if [ -n "$api_key" ]; then
            echo "✅ $app_name API Key: $api_key"
            return 0
        fi
    fi
    echo "❌ $app_name: Config file not found or API key not set"
    return 1
}

echo "📝 Extracting API Keys..."
echo ""

# Extract Radarr API key
extract_api_key "Radarr" "./config/radarr/config.xml"

# Extract Sonarr API key
extract_api_key "Sonarr" "./config/sonarr/config.xml"

# Extract Prowlarr API key
extract_api_key "Prowlarr" "./config/prowlarr/config.xml"

echo ""
echo "💡 How to get API keys manually:"
echo ""
echo "1. Radarr: http://your-ip:7878/settings/general"
echo "   - Scroll to 'Security' section"
echo "   - Copy the API Key"
echo ""
echo "2. Sonarr: http://your-ip:8989/settings/general"
echo "   - Scroll to 'Security' section"
echo "   - Copy the API Key"
echo ""
echo "3. Prowlarr: http://your-ip:9696/settings/general"
echo "   - Scroll to 'Security' section"
echo "   - Copy the API Key"
echo ""
echo "📝 Add these keys to your .env file:"
echo "   nano .env"
echo ""
echo "Then restart the exporters:"
echo "   docker compose restart exportarr-radarr exportarr-sonarr exportarr-prowlarr"
