#!/usr/bin/env bash

set -euo pipefail

# --- Variables de Entorno ---
: "${OUTPUT_FILE:?Error: OUTPUT_FILE not set}"
: "${SOURCE_URL:?Error: SOURCE_URL not set}"
: "${ACESERVE_URL:?Error: ACESERVE_URL not set}"
: "${PUSHGATEWAY_URL:?Error: PUSHGATEWAY_URL not set}"

# El archivo se guardará en /app/ (que es scripts/acestream-updater/ en tu Raspi)
METRICS_FILE="/app/metrics.env"
TEMP_RAW="/tmp/raw.m3u"
TEMP_NEW="/tmp/new.m3u"

# Inicializar variables para evitar el error "unbound variable"
SUCCESS_CHANGES=0
SUCCESS_NO_CHANGES=0
ERRORS=0
TOTAL_RUNS=0

# Cargar estado si existe
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

# Proceso de descarga
if ! curl -fsSL --connect-timeout 15 --max-time 60 "${SOURCE_URL}" -o "${TEMP_RAW}"; then
    echo "Error: Download failed." >&2
    ERRORS=$((ERRORS + 1))
    save_metrics_state && push_metrics
    exit 1
fi

sed "s|acestream://|${ACESERVE_URL}|g" "${TEMP_RAW}" > "${TEMP_NEW}"

if [[ -f "${OUTPUT_FILE}" ]] && cmp -s "${OUTPUT_FILE}" "${TEMP_NEW}"; then
    echo "No changes detected."
    SUCCESS_NO_CHANGES=$((SUCCESS_NO_CHANGES + 1))
else
    mv "${TEMP_NEW}" "${OUTPUT_FILE}"
    echo "Changes applied."
    SUCCESS_CHANGES=$((SUCCESS_CHANGES + 1))
fi

save_metrics_state
push_metrics
echo "Successfully finished."