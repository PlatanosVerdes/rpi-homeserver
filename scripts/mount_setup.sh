#!/bin/bash

# --- CHECK INPUT ---
if [ -z "$1" ]; then
    echo "ERROR: UUID is missing."
    echo "Usage: sudo bash $0 <UUID_OF_YOUR_DISK>"
    echo "You can find the UUID by running: lsblk -f"
    exit 1
fi
# ==========================================
# SSD SETUP & MOUNT SCRIPT
# ==========================================
# This script configures the specific SSD for the media server.

# --- CONFIGURATION ---
DISK_UUID=$1
MOUNT_POINT="/mnt/ssd"
USER_ID=1000  # Default 'raspi' user ID
GROUP_ID=1000 # Default 'raspi' group ID

echo "--- 1. Preparing Mount Point ---"
# Check if the folder exists, if not, create it
if [ ! -d "$MOUNT_POINT" ]; then
    echo "[+] Creating directory: $MOUNT_POINT"
    sudo mkdir -p "$MOUNT_POINT"
fi

echo "--- 2. Checking Configuration (fstab) ---"
# Check if UUID exists in fstab to avoid duplicates
if grep -q "$DISK_UUID" /etc/fstab; then
    echo "[OK] UUID is already in /etc/fstab. Skipping addition."
else
    echo "[+] Adding drive to startup configuration..."
    # Append the configuration line to /etc/fstab safely
    echo "UUID=$DISK_UUID  $MOUNT_POINT  ext4  defaults,noatime  0  2" | sudo tee -a /etc/fstab
fi

echo "--- 3. Mounting Drive ---"
sudo systemctl daemon-reload
sudo mount -a

# Verification check
if mountpoint -q "$MOUNT_POINT"; then
    echo "[SUCCESS] Drive is correctly mounted at $MOUNT_POINT"
else
    echo "[ERROR] Failed to mount. Please check the UUID."
    exit 1
fi

echo "--- 4. Creating Folder Structure (Atomic Moves) ---"
# Creating the unified /data structure for instant file moves
# This structure is required for Hardlinks (TRaSH Guides standard)
echo "[+] Creating directories..."
sudo mkdir -p "$MOUNT_POINT/data/torrents/movies"
sudo mkdir -p "$MOUNT_POINT/data/torrents/tv"
sudo mkdir -p "$MOUNT_POINT/data/media/movies"
sudo mkdir -p "$MOUNT_POINT/data/media/tv"

echo "--- 5. Setting Permissions ---"
# CRITICAL: Giving ownership to the 'raspi' user (1000) so Docker can write
echo "[+] Setting ownership to user $USER_ID..."
sudo chown -R $USER_ID:$GROUP_ID "$MOUNT_POINT"
sudo chmod -R 775 "$MOUNT_POINT"

echo "--- [DONE] Setup Complete! Ready for Docker ---"