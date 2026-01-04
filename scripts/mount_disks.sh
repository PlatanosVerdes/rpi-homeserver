#!/bin/bash
set -e

# Load configuration from .env
if [ -f .env ]; then
    source .env
else
    echo "❌ .env file not found!"
    exit 1
fi

DATA_PATH="${DATA_PATH:-/mnt/data}"

echo "💾 Setting up mount point for external disks..."

# Create mount point
sudo mkdir -p ${DATA_PATH}

echo ""
echo "📝 To mount an external disk automatically:"
echo ""
echo "1. Connect your USB/SATA disk to the Raspberry Pi"
echo "2. Identify the disk with: lsblk"
echo "3. Get the disk UUID: sudo blkid"
echo "4. Edit /etc/fstab and add a line like:"
echo "   UUID=your-uuid-here ${DATA_PATH} ext4 defaults,nofail 0 2"
echo "5. Mount the disk: sudo mount -a"
echo ""
echo "💡 If you don't have an external disk, ${DATA_PATH} will use SD card storage"
echo ""
echo "💡 For RAID configuration, use the setup_raid.sh script instead"

# Create folder structure
sudo mkdir -p ${DATA_PATH}/media/{movies,series}
sudo mkdir -p ${DATA_PATH}/downloads/{complete,incomplete}
sudo mkdir -p ${DATA_PATH}/nextcloud

echo "✅ Folder structure created at ${DATA_PATH}"
