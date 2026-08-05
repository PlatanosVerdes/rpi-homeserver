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
    # One line per inode. A grabbed file and its imported copy are usually the same inode
    # (the *arrs hardlink), so listing both paths shows the same bytes twice and doubles the total.
    # 14 of the 30 largest files here have more than one link. Sorting paths in reverse before the
    # dedupe keeps the library path (films/, tv/) over the downloads/ one, which reads better.
    $RUN find "$TARGET" -type f -printf '%i\t%s\t%p\n' 2>/dev/null \
        | sort -t$'\t' -k1,1 -k3,3r \
        | awk -F'\t' '!seen[$1]++' \
        | sort -t$'\t' -k2,2rn | head -n "$TOP" \
        | while IFS=$'\t' read -r inode size path; do
            rel="${path#$TARGET/}"; root="${rel%%/*}"
            p=$(printf '%s' "$path" | esc)
            r=$(printf '%s' "$root" | esc)
            echo "disk_file_bytes{root=\"$r\",path=\"$p\"} $size"
          done

    # du counts each inode once per invocation, so a hardlinked file lands in whichever folder du
    # walks first instead of being counted in both. That is the behaviour we want here.
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
