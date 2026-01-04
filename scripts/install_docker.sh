#!/bin/bash
# This script installs Docker and Docker Compose on Raspberry Pi OS

set -e

echo "Installing Docker..."
curl -fsSL https://get.docker.com | sh

echo "Adding user to docker group..."
sudo usermod -aG docker $USER

echo "Enabling Docker at boot..."
sudo systemctl enable docker
sudo systemctl start docker

echo "Docker installation completed."
echo "Installing Docker Compose..."
sudo apt-get update
sudo apt-get install -y docker-compose-plugin

echo "✅ Docker and Docker Compose installed successfully"
echo "⚠️  You need to log out and back in for changes to take effect"
echo "   Or run: newgrp docker"

