#!/bin/bash
# Export the largest paths under the data disk to node-exporter's textfile
# collector, so Grafana can show a "biggest files / dirs" table.
#
# A full find/du over a ~1 TB disk is heavy on I/O, so this is meant to run from
# cron only every few hours (see scripts/crontab), niced and ionice'd to stay out
# of the way. Writes atomically to the textfile collector directory that
# node-exporter already watches.

TARGET="${1:-/mnt/data}"
OUT="/var/lib/node_exporter/textfile_collector/disk_usage.prom"
TMP="$(mktemp)"
TOP="${TOP:-30}"

# Escape backslashes and double quotes for Prometheus label values.
esc() { sed 's/\\/\\\\/g; s/"/\\"/g'; }

RUN="nice -n 19 ionice -c3"

{
    echo "# HELP disk_file_bytes Size in bytes of the largest individual files on the data disk."
    echo "# TYPE disk_file_bytes gauge"
    $RUN find "$TARGET" -type f -printf '%s\t%p\n' 2>/dev/null | sort -rn | head -n "$TOP" \
        | while IFS=$'\t' read -r size path; do
            rel="${path#$TARGET/}"; root="${rel%%/*}"
            p=$(printf '%s' "$path" | esc)
            r=$(printf '%s' "$root" | esc)
            echo "disk_file_bytes{root=\"$r\",path=\"$p\"} $size"
          done

    echo "# HELP disk_root_bytes Size in bytes of each top-level folder on the data disk."
    echo "# TYPE disk_root_bytes gauge"
    $RUN du -b --max-depth=1 "$TARGET" 2>/dev/null | sort -rn \
        | while IFS=$'\t' read -r size path; do
            [ "$path" = "$TARGET" ] && continue
            r=$(printf '%s' "${path#$TARGET/}" | esc)
            echo "disk_root_bytes{root=\"$r\"} $size"
          done

    echo "# HELP disk_usage_scrape_timestamp_seconds Unix time this metrics file was generated."
    echo "# TYPE disk_usage_scrape_timestamp_seconds gauge"
    echo "disk_usage_scrape_timestamp_seconds $(date +%s)"
} > "$TMP"

mv "$TMP" "$OUT"
chmod 644 "$OUT"
