#!/bin/bash
set -e

# RAID Monitoring Setup Script
# Configures mdadm to send email alerts when RAID issues occur

echo "🔔 Setting up RAID monitoring..."

# Check if mdadm is installed
if ! command -v mdadm &> /dev/null; then
    echo "❌ mdadm is not installed. Run setup_raid.sh first."
    exit 1
fi

# Check if RAID array exists
if [ ! -e /dev/md0 ]; then
    echo "❌ No RAID array found at /dev/md0"
    exit 1
fi

# Configure mdadm monitoring
echo "📧 Configuring mdadm monitoring..."

# Backup original config
if [ -f /etc/mdadm/mdadm.conf ]; then
    cp /etc/mdadm/mdadm.conf /etc/mdadm/mdadm.conf.backup
fi

# Ask for email (optional)
read -p "Enter email for alerts (leave empty to skip): " EMAIL

if [ -n "$EMAIL" ]; then
    # Configure email alerts
    if ! grep -q "MAILADDR" /etc/mdadm/mdadm.conf; then
        echo "MAILADDR $EMAIL" >> /etc/mdadm/mdadm.conf
    else
        sed -i "s/^MAILADDR.*/MAILADDR $EMAIL/" /etc/mdadm/mdadm.conf
    fi
    echo "✅ Email alerts configured for: $EMAIL"
fi

# Enable mdmonitor service
systemctl enable mdmonitor
systemctl restart mdmonitor

# Create a simple check script
cat > /usr/local/bin/raid-check << 'EOF'
#!/bin/bash
# Simple RAID health check

STATUS=$(cat /proc/mdstat | grep -A 2 md0)
FAILED=$(mdadm --detail /dev/md0 | grep -c "failed")

if [ "$FAILED" -gt 0 ]; then
    echo "⚠️ RAID WARNING: Disk failure detected!"
    echo "$STATUS"
    exit 1
else
    echo "✅ RAID is healthy"
fi
EOF

chmod +x /usr/local/bin/raid-check

# Add to crontab for daily checks
if ! crontab -l 2>/dev/null | grep -q "raid-check"; then
    (crontab -l 2>/dev/null; echo "0 8 * * * /usr/local/bin/raid-check") | crontab -
    echo "✅ Daily RAID check scheduled at 8:00 AM"
fi

echo ""
echo "✅ RAID monitoring configured!"
echo ""
echo "📊 Useful commands:"
echo "  - Check RAID status: sudo raid-check"
echo "  - View detailed info: sudo mdadm --detail /dev/md0"
echo "  - Monitor in real-time: watch cat /proc/mdstat"
echo ""
echo "💡 The system will check RAID health daily and alert if issues are found"
