#!/usr/bin/env bash

set -euo pipefail

: "${OUTPUT_FILE:?Error: OUTPUT_FILE not set}"
: "${SOURCE_URLS:?Error: SOURCE_URLS not set}"
: "${ACESERVE_URL:?Error: ACESERVE_URL not set}"
: "${PUSHGATEWAY_URL:?Error: PUSHGATEWAY_URL not set}"
: "${JELLYFIN_URL:?Error: JELLYFIN_URL not set}"
: "${JELLYFIN_API_KEY:?Error: JELLYFIN_API_KEY not set}"

METRICS_FILE="/app/metrics.env"
TEMP_COMBINED="/tmp/combined.m3u"
TEMP_NEW="/tmp/new.m3u"

SUCCESS_CHANGES=0
SUCCESS_NO_CHANGES=0
ERRORS=0
TOTAL_RUNS=0

if [[ -f "$METRICS_FILE" ]]; then
    source "$METRICS_FILE" || true
fi

TOTAL_RUNS=$((TOTAL_RUNS + 1))

save_metrics_state() {
    cat <<EOF > "$METRICS_FILE"
SUCCESS_CHANGES=$SUCCESS_CHANGES
SUCCESS_NO_CHANGES=$SUCCESS_NO_CHANGES
ERRORS=$ERRORS
TOTAL_RUNS=$TOTAL_RUNS
EOF
}

push_metrics() {
    cat <<EOF | curl -fsSL --connect-timeout 5 --data-binary @- "${PUSHGATEWAY_URL}/metrics/job/acestream_updater"
# HELP acestream_run_total Total executions
# TYPE acestream_run_total counter
acestream_run_total $TOTAL_RUNS
# HELP acestream_updates_with_changes Updates with changes
# TYPE acestream_updates_with_changes counter
acestream_updates_with_changes $SUCCESS_CHANGES
# HELP acestream_updates_no_changes Executions without changes
# TYPE acestream_updates_no_changes counter
acestream_updates_no_changes $SUCCESS_NO_CHANGES
# HELP acestream_update_errors Total errors
# TYPE acestream_update_errors counter
acestream_update_errors $ERRORS
# HELP acestream_last_run_timestamp Last run timestamp
# TYPE acestream_last_run_timestamp gauge
acestream_last_run_timestamp $(date +%s)
EOF
}

echo "Starting execution #$TOTAL_RUNS"

# Download all sources and combine, skipping the #EXTM3U header from each
IFS=',' read -ra URLS <<< "$SOURCE_URLS"
DOWNLOAD_ERRORS=0
TEMP_RAW="/tmp/raw_$$.m3u"

: > "$TEMP_COMBINED"  # empty file
for URL in "${URLS[@]}"; do
    URL="${URL// /}"  # trim spaces
    [[ -z "$URL" ]] && continue
    echo "Downloading: $URL"
    if curl -fsSL --connect-timeout 15 --max-time 60 "$URL" -o "$TEMP_RAW" 2>/dev/null; then
        grep -v "^#EXTM3U" "$TEMP_RAW" >> "$TEMP_COMBINED" || true
        echo "  OK"
    else
        echo "  Warning: Failed to download $URL" >&2
        DOWNLOAD_ERRORS=$((DOWNLOAD_ERRORS + 1))
    fi
done
rm -f "$TEMP_RAW"

if [[ $DOWNLOAD_ERRORS -gt 0 ]]; then
    ERRORS=$((ERRORS + DOWNLOAD_ERRORS))
    if [[ $DOWNLOAD_ERRORS -eq ${#URLS[@]} ]]; then
        echo "Error: All downloads failed." >&2
        save_metrics_state && push_metrics
        exit 1
    fi
fi

# Deduplicate by acestream hash and replace acestream:// with ACESERVE_URL
{
    echo "#EXTM3U"
    awk -v aceserve_url="$ACESERVE_URL" '
        /^#EXTINF/ { extinf = $0; next }
        /^acestream:\/\// {
            hash = substr($0, 13)
            if (!(hash in seen)) {
                seen[hash] = 1
                print extinf
                sub(/^acestream:\/\//, aceserve_url)
                print
            }
        }
    ' "$TEMP_COMBINED"
} > "$TEMP_NEW"

TOTAL_IN=$(grep -c "^acestream://" "$TEMP_COMBINED" 2>/dev/null || echo 0)
TOTAL_OUT=$(grep -v "^#" "$TEMP_NEW" | grep -c "." 2>/dev/null || echo 0)
echo "Channels: $TOTAL_IN total from all sources, $((TOTAL_OUT / 2)) after dedup"

if [[ -f "${OUTPUT_FILE}" ]] && cmp -s "${OUTPUT_FILE}" "${TEMP_NEW}"; then
    echo "No changes detected."
    SUCCESS_NO_CHANGES=$((SUCCESS_NO_CHANGES + 1))
else
    mv "${TEMP_NEW}" "${OUTPUT_FILE}"
    echo "Changes applied."
    SUCCESS_CHANGES=$((SUCCESS_CHANGES + 1))
    if curl -fsSL --connect-timeout 5 -X POST \
        -H "X-Emby-Token: ${JELLYFIN_API_KEY}" \
        "${JELLYFIN_URL}/ScheduledTasks/Running/0c9ee3a88fc15547c6852205480da1fd" > /dev/null 2>&1; then
        echo "Jellyfin channel refresh triggered."
    else
        echo "Warning: Jellyfin refresh failed (non-critical)." >&2
    fi
fi

save_metrics_state
push_metrics
echo "Successfully finished."
