#!/bin/bash
# This script installs Docker and Docker Compose on Raspberry Pi OS

set -e

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m'

# Check if Docker is already installed
if command -v docker &> /dev/null; then
    echo -e "${GREEN}✅ Docker is already installed${NC}"
    docker --version
    
    # Check if user is in docker group
    if groups $USER | grep -q '\bdocker\b'; then
        echo -e "${GREEN}✅ User is already in docker group${NC}"
    else
        echo -e "${YELLOW}⚠️  Adding user to docker group...${NC}"
        sudo usermod -aG docker $USER
        echo -e "${YELLOW}⚠️  You need to log out and back in for docker group changes to take effect${NC}"
    fi
    
    # Check if docker-compose plugin is installed
    if docker compose version &> /dev/null; then
        echo -e "${GREEN}✅ Docker Compose is already installed${NC}"
        docker compose version
    else
        echo -e "${BLUE}📦 Installing Docker Compose plugin...${NC}"
        sudo apt-get update -qq
        sudo apt-get install -y docker-compose-plugin
        echo -e "${GREEN}✅ Docker Compose installed${NC}"
    fi
    
    exit 0
fi

echo -e "${BLUE}📦 Installing Docker...${NC}"
curl -fsSL https://get.docker.com | sh

echo -e "${BLUE}👤 Adding user to docker group...${NC}"
sudo usermod -aG docker $USER

echo -e "${BLUE}🚀 Enabling Docker at boot...${NC}"
sudo systemctl enable docker
sudo systemctl start docker

echo -e "${BLUE}📦 Installing Docker Compose...${NC}"
sudo apt-get update -qq
sudo apt-get install -y docker-compose-plugin

echo -e "${GREEN}✅ Docker and Docker Compose installed successfully${NC}"
echo -e "${YELLOW}⚠️  You need to log out and back in for changes to take effect${NC}"
echo -e "${YELLOW}   Or run: newgrp docker${NC}"

