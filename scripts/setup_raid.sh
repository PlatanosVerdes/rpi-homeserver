#!/bin/bash
set -e

# RAID1 Setup Script for Raspberry Pi
# This script helps set up a RAID1 (mirror) array to protect against disk failure

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${BLUE}🛡️  RAID1 Setup for Home Server${NC}"
echo -e "${YELLOW}⚠️  WARNING: This will ERASE ALL DATA on the selected disks!${NC}"
echo ""

# Check if running as root
if [[ $EUID -ne 0 ]]; then
   echo -e "${RED}❌ This script must be run as root (use sudo)${NC}"
   exit 1
fi

# Install mdadm if not present
if ! command -v mdadm &> /dev/null; then
    echo -e "${BLUE}📦 Installing mdadm...${NC}"
    apt-get update -qq
    apt-get install -y mdadm
    echo -e "${GREEN}✅ mdadm installed${NC}"
else
    echo -e "${GREEN}✅ mdadm is already installed${NC}"
fi

# Show available disks
echo -e "${BLUE}📀 Available disks:${NC}"
lsblk -o NAME,SIZE,TYPE,MOUNTPOINT | grep -E "disk|part"
echo ""

# Prompt for disk selection
echo -e "${YELLOW}Please identify the two disks you want to use for RAID1${NC}"
echo "Example: sda and sdb (without /dev/ prefix)"
echo ""
read -p "First disk (e.g., sda): " DISK1
read -p "Second disk (e.g., sdb): " DISK2

# Validate input
if [ -z "$DISK1" ] || [ -z "$DISK2" ]; then
    echo -e "${RED}❌ Both disks must be specified${NC}"
    exit 1
fi

if [ "$DISK1" == "$DISK2" ]; then
    echo -e "${RED}❌ Disks must be different${NC}"
    exit 1
fi

if [ ! -b "/dev/$DISK1" ] || [ ! -b "/dev/$DISK2" ]; then
    echo -e "${RED}❌ One or both disks don't exist${NC}"
    exit 1
fi

# Final confirmation
echo ""
echo -e "${RED}⚠️  FINAL WARNING ⚠️${NC}"
echo -e "This will DESTROY all data on:"
echo -e "  - /dev/$DISK1"
echo -e "  - /dev/$DISK2"
echo ""
read -p "Type 'YES' to continue: " CONFIRM

if [ "$CONFIRM" != "YES" ]; then
    echo -e "${YELLOW}Aborted by user${NC}"
    exit 0
fi

# Unmount disks if mounted
echo -e "${BLUE}🔧 Unmounting disks if mounted...${NC}"
umount /dev/${DISK1}* 2>/dev/null || true
umount /dev/${DISK2}* 2>/dev/null || true

# Create RAID1 array
echo -e "${BLUE}🛠️  Creating RAID1 array...${NC}"
mdadm --create --verbose /dev/md0 \
    --level=1 \
    --raid-devices=2 \
    /dev/$DISK1 /dev/$DISK2

# Save RAID configuration
echo -e "${BLUE}💾 Saving RAID configuration...${NC}"
mdadm --detail --scan >> /etc/mdadm/mdadm.conf
update-initramfs -u

# Format the RAID array
echo -e "${BLUE}🔨 Formatting RAID array with ext4...${NC}"
mkfs.ext4 -F /dev/md0

# Get UUID
RAID_UUID=$(blkid -s UUID -o value /dev/md0)
echo -e "${GREEN}✅ RAID array created with UUID: $RAID_UUID${NC}"

# Create mount point
mkdir -p /mnt/data

# Add to fstab
echo -e "${BLUE}📝 Adding to /etc/fstab...${NC}"
if ! grep -q "/dev/md0" /etc/fstab; then
    echo "UUID=$RAID_UUID /mnt/data ext4 defaults,nofail 0 2" >> /etc/fstab
    echo -e "${GREEN}✅ Added to /etc/fstab${NC}"
else
    echo -e "${YELLOW}⚠️  Entry already exists in /etc/fstab${NC}"
fi

# Mount the array
echo -e "${BLUE}📌 Mounting RAID array...${NC}"
mount /mnt/data

# Verify
echo ""
echo -e "${GREEN}✅ RAID1 setup completed!${NC}"
echo ""
echo "📊 RAID Status:"
mdadm --detail /dev/md0

echo ""
echo "📁 Mount point: /mnt/data"
df -h /mnt/data

echo ""
echo -e "${BLUE}💡 Useful commands:${NC}"
echo "  - Check RAID status: sudo mdadm --detail /dev/md0"
echo "  - Monitor RAID: cat /proc/mdstat"
echo "  - Check disk health: sudo smartctl -a /dev/$DISK1"
echo ""
echo -e "${YELLOW}⚠️  Important:${NC}"
echo "  - RAID1 will continue syncing in the background"
echo "  - You can use the array while it syncs"
echo "  - Monitor progress with: watch cat /proc/mdstat"
