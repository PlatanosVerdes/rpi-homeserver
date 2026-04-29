#!/bin/bash
# Pushes cal-bridge secret expiry metric to Pushgateway.
# Cron: 0 9 * * * /home/raspi/rpi-homeserver/scripts/cal-bridge-metrics.sh

source "$(dirname "$0")/../.env" 2>/dev/null

STATUS=$(curl -s --max-time 5 "http://localhost:8091/status?key=${CAL_API_KEY}")
DAYS=$(echo "$STATUS" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('ms_secret',{}).get('days_remaining',-1))" 2>/dev/null || echo -1)

cat <<EOF | curl -s --data-binary @- http://localhost:9091/metrics/job/cal-bridge/instance/secrets
# HELP ms_client_secret_days_remaining Days until Microsoft client secret expires
# TYPE ms_client_secret_days_remaining gauge
ms_client_secret_days_remaining $DAYS
EOF
