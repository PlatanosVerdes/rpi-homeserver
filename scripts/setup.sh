#!/bin/bash
set -e

# Raspberry Pi Home Server Setup Script
# Single script to setup everything

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

cd "$PROJECT_DIR"

# Load .env
if [ -f .env ]; then
    source .env
else
    echo -e "${RED}❌ .env file not found!${NC}"
    echo "Copy .env.example to .env and configure it first:"
    echo "  cp .env.example .env"
    echo "  nano .env"
    exit 1
fi

HOSTNAME="${HOSTNAME:-banana}"
DOMAIN="${DOMAIN:-banana.lan}"
DATA_PATH="${DATA_PATH:-/mnt/data}"

echo -e "${BLUE}🍌 Banana Home Server Setup${NC}"
echo "================================================"
echo ""

# Check if running as root
if [[ $EUID -eq 0 ]]; then
   echo -e "${YELLOW}⚠️  Don't run this script as root. Use your regular user.${NC}"
   exit 1
fi

# Menu
echo "What do you want to do?"
echo ""
echo "  1) Full setup (first time installation)"
echo "  2) Setup DNS (local domain names)"
echo "  3) Setup RAID (disk mirroring)"
echo "  4) Setup Grafana dashboards"
echo "  5) Get API keys from *arr apps"
echo "  6) Check containers health"
echo "  7) Fix permissions"
echo ""
read -p "Enter option (1-7): " option

case $option in
    1)
        echo -e "${GREEN}🚀 Starting full setup...${NC}"
        echo ""
        
        # Install Docker
        echo -e "${BLUE}📦 Step 1/6: Installing Docker...${NC}"
        bash "$SCRIPT_DIR/install_docker.sh"
        
        # Mount disks
        echo -e "${BLUE}💾 Step 2/6: Mounting disks...${NC}"
        bash "$SCRIPT_DIR/mount_disks.sh"
        
        # Create directories
        echo -e "${BLUE}📁 Step 3/6: Creating directories...${NC}"
        sudo mkdir -p ${DATA_PATH}/media/{movies,series}
        sudo mkdir -p ${DATA_PATH}/downloads/{complete,incomplete}
        sudo mkdir -p ${DATA_PATH}/nextcloud
        mkdir -p config/{plex,pihole/{etc-pihole,etc-dnsmasq},qbittorrent,prowlarr,radarr,sonarr,nextcloud,tailscale,grafana,prometheus,tautulli}
        echo -e "${GREEN}✅ Directories created${NC}"
        
        # Set permissions
        echo -e "${BLUE}🔐 Step 4/6: Setting permissions...${NC}"
        bash "$SCRIPT_DIR/permissions.sh"
        
        # Start services
        echo -e "${BLUE}🐳 Step 5/6: Starting Docker services...${NC}"
        
        # Check if we can access docker socket
        if ! docker ps &>/dev/null; then
            echo -e "${YELLOW}⚠️  Docker permissions not active in this session${NC}"
            echo -e "${YELLOW}   Using 'sg docker' to run with docker group...${NC}"
            sg docker -c "docker compose up -d"
        else
            docker compose up -d
        fi
        echo -e "${GREEN}✅ Services started${NC}"
        
        # Wait for Pi-hole to be ready and set password
        echo -e "${BLUE}🔐 Configuring Pi-hole password...${NC}"
        echo "Waiting for Pi-hole to start..."
        sleep 10
        if docker ps | grep -q pihole; then
            docker exec pihole pihole setpassword "${PIHOLE_PASSWORD}" > /dev/null 2>&1 || true
            echo -e "${GREEN}✅ Pi-hole password configured${NC}"
        fi
        
        # Setup DNS
        echo -e "${BLUE}🌐 Step 6/6: Configuring DNS...${NC}"
        bash "$SCRIPT_DIR/setup_dns.sh"
        
        echo ""
        echo -e "${GREEN}🎉 Setup complete!${NC}"
        echo ""
        echo "📝 Next steps:"
        echo "  1. Configure your router to use this Pi as DNS: $(hostname -I | awk '{print $1}')"
        echo "  2. Access Grafana: http://grafana.${DOMAIN}:3000"
        echo "  3. Setup Grafana dashboards: ./scripts/setup.sh (option 4)"
        ;;
        
    2)
        echo -e "${BLUE}🌐 Setting up DNS...${NC}"
        bash "$SCRIPT_DIR/setup_dns.sh"
        ;;
        
    3)
        echo -e "${BLUE}🛡️  RAID Setup${NC}"
        echo ""
        echo -e "${YELLOW}⚠️  This will ERASE ALL DATA on selected disks!${NC}"
        echo ""
        bash "$SCRIPT_DIR/setup_raid.sh"
        echo ""
        echo "Setting up RAID monitoring..."
        bash "$SCRIPT_DIR/setup_raid_monitoring.sh"
        ;;
        
    4)
        echo -e "${BLUE}📊 Setting up Grafana...${NC}"
        bash "$SCRIPT_DIR/setup_grafana.sh"
        ;;
        
    5)
        echo -e "${BLUE}🔑 Extracting API Keys...${NC}"
        bash "$SCRIPT_DIR/get_api_keys.sh"
        ;;
        
    6)
        echo -e "${BLUE}🏥 Container Health Status${NC}"
        bash "$SCRIPT_DIR/check_health.sh"
        ;;
        
    7)
        echo -e "${BLUE}🔐 Fixing permissions...${NC}"
        bash "$SCRIPT_DIR/permissions.sh"
        ;;
        
    *)
        echo -e "${RED}Invalid option${NC}"
        exit 1
        ;;
esac
