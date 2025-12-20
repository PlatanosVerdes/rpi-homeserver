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
DOCKER_COMPOSE_VERSION="1.29.2"
curl -L "https://github.com/docker/compose/releases/download/${DOCKER_COMPOSE_VERSION}/docker-compose-$(uname -s)-$(uname -m)" -o /usr/local/bin/docker-compose
chmod +x /usr/local/bin/docker-compose

