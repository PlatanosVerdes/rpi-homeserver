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

declare -A SOURCE_HTTP_CODES   # key=URL, value=HTTP_CODE
declare -A CHANNEL_HEALTH_CODES # key=channel name, value=HTTP code from acestream engine

save_metrics_state() {
    cat <<EOF > "$METRICS_FILE"
SUCCESS_CHANGES=$SUCCESS_CHANGES
SUCCESS_NO_CHANGES=$SUCCESS_NO_CHANGES
ERRORS=$ERRORS
TOTAL_RUNS=$TOTAL_RUNS
EOF
}

push_metrics() {
    {
        cat <<EOF
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
# HELP acestream_unique_channels Current number of unique channels after dedup
# TYPE acestream_unique_channels gauge
acestream_unique_channels $UNIQUE_CHANNELS
# HELP acestream_jellyfin_refresh_http_code HTTP code returned by Jellyfin refresh API (0 = not called)
# TYPE acestream_jellyfin_refresh_http_code gauge
acestream_jellyfin_refresh_http_code $JELLYFIN_REFRESH_HTTP_CODE
# HELP acestream_source_http_code HTTP response code per source URL (0 = connection failed)
# TYPE acestream_source_http_code gauge
EOF
        for host in "${!SOURCE_HTTP_CODES[@]}"; do
            echo "acestream_source_http_code{url=\"${host}\"} ${SOURCE_HTTP_CODES[$host]}"
        done
        if [[ ${#CHANNEL_HEALTH_CODES[@]} -gt 0 ]]; then
            echo "# HELP acestream_channel_health HTTP code from acestream getstream (302=OK, 0=timeout)"
            echo "# TYPE acestream_channel_health gauge"
            for name in "${!CHANNEL_HEALTH_CODES[@]}"; do
                safe_name=$(printf '%s' "$name" | sed 's/\\/\\\\/g; s/"/\\"/g')
                echo "acestream_channel_health{name=\"${safe_name}\"} ${CHANNEL_HEALTH_CODES[$name]}"
            done
        fi
    } | curl -fsSL --connect-timeout 5 --data-binary @- "${PUSHGATEWAY_URL}/metrics/job/acestream_updater"
}

check_channel_health() {
    local name=""
    echo "Running channel health checks..."
    while IFS= read -r line; do
        if [[ "$line" =~ ^#EXTINF ]]; then
            name="$(echo "$line" | sed 's/.*,//')"
        elif [[ "$line" =~ ^http ]]; then
            if [[ -n "$name" ]]; then
                local code
                # 302 = engine accepted the hash and is redirecting to stream = OK
                # We use --max-redirs 0 to stop after the redirect, no stream is opened
                code=$(curl -s -o /dev/null -w "%{http_code}" \
                    --connect-timeout 3 --max-time 5 --max-redirs 0 \
                    "$line" 2>/dev/null) || code=0
                CHANNEL_HEALTH_CODES["$name"]=$code
                echo "  ${name}: HTTP ${code}"
            fi
            name=""
        fi
    done < "$OUTPUT_FILE"
}

echo "Starting execution #$TOTAL_RUNS"

# Download all sources and combine, skipping the #EXTM3U header from each
IFS=',' read -ra URLS <<< "$SOURCE_URLS"
DOWNLOAD_ERRORS=0
TEMP_RAW="/tmp/raw_$$.m3u"

: > "$TEMP_COMBINED"  # empty file
for i in "${!URLS[@]}"; do
    URL="${URLS[$i]// /}"  # trim spaces
    [[ -z "$URL" ]] && continue
    echo "Downloading: $URL"
    HTTP_CODE=$(curl -sL --connect-timeout 15 --max-time 60 -o "$TEMP_RAW" -w "%{http_code}" "$URL" 2>/dev/null || echo 0)
    HOST=$(echo "$URL" | awk -F/ '{print $3}')
    SOURCE_HTTP_CODES["$HOST"]=$HTTP_CODE
    if [[ "$HTTP_CODE" == "200" ]]; then
        grep -v "^#EXTM3U" "$TEMP_RAW" >> "$TEMP_COMBINED" || true
        echo "  OK (200)"
    else
        echo "  Warning: Failed (HTTP $HTTP_CODE)" >&2
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
UNIQUE_CHANNELS=$(( TOTAL_OUT / 2 ))
echo "Channels: $TOTAL_IN total from all sources, $UNIQUE_CHANNELS after dedup"

JELLYFIN_REFRESH_HTTP_CODE=0
if [[ -f "${OUTPUT_FILE}" ]] && cmp -s "${OUTPUT_FILE}" "${TEMP_NEW}"; then
    echo "No changes detected."
    SUCCESS_NO_CHANGES=$((SUCCESS_NO_CHANGES + 1))
else
    mv "${TEMP_NEW}" "${OUTPUT_FILE}"
    echo "Changes applied."
    SUCCESS_CHANGES=$((SUCCESS_CHANGES + 1))
    JELLYFIN_REFRESH_HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 -X POST \
        -H "X-Emby-Token: ${JELLYFIN_API_KEY}" \
        "${JELLYFIN_URL}/ScheduledTasks/Running/0c9ee3a88fc15547c6852205480da1fd" 2>/dev/null || echo 0)
    echo "Jellyfin refresh HTTP code: $JELLYFIN_REFRESH_HTTP_CODE"
fi

if [[ -f "$OUTPUT_FILE" ]]; then
    check_channel_health
fi
save_metrics_state
push_metrics
echo "Successfully finished."
