#!/bin/bash
# Export what zram actually costs, for node_exporter's textfile collector.
#
# node_exporter reports swap the same way whether it lives on a disk or in compressed RAM, so the
# stock "SWAP used" gauge reads 87% and turns red while nothing is wrong: this swap is /dev/zram0,
# and a full zram is zram working. What it does not report is the only number that matters here,
# how much real RAM those compressed pages occupy — currently 0.56 GB holding 1.65 GB of pages.
#
# Values come from /sys/block/zram0/mm_stat, whose columns are, in order:
#   orig_data_size compr_data_size mem_used_total mem_limit mem_used_max same_pages
#   pages_compacted huge_pages
#
# Cheap enough to run every minute: one read of a sysfs file, no processes walked, no disk touched.
set -uo pipefail

OUT_DIR="/var/lib/node_exporter/textfile_collector"
OUT="$OUT_DIR/zram.prom"

[[ -d "$OUT_DIR" ]] || exit 0

emit() {
    for dev in /sys/block/zram*; do
        [[ -r "$dev/mm_stat" ]] || continue
        name=$(basename "$dev")
        read -r orig compr used _limit used_max _same _compacted _huge < "$dev/mm_stat" || continue

        echo "zram_original_bytes{device=\"$name\"} $orig"
        echo "zram_compressed_bytes{device=\"$name\"} $compr"
        echo "zram_memory_used_bytes{device=\"$name\"} $used"
        echo "zram_memory_used_max_bytes{device=\"$name\"} $used_max"
        # Ratio is derived rather than left to the dashboard: dividing by zero when the device is
        # empty would render as an ugly NaN on a panel that exists to be reassuring.
        if [[ "$used" -gt 0 ]]; then
            awk -v o="$orig" -v u="$used" \
                "BEGIN { printf \"zram_compression_ratio{device=\\\"$name\\\"} %.3f\n\", o/u }"
        else
            echo "zram_compression_ratio{device=\"$name\"} 1"
        fi
    done
}

{
    echo "# HELP zram_original_bytes Uncompressed size of the data held in zram"
    echo "# TYPE zram_original_bytes gauge"
    echo "# HELP zram_compressed_bytes Size of that data after compression"
    echo "# TYPE zram_compressed_bytes gauge"
    echo "# HELP zram_memory_used_bytes Real RAM zram occupies, including its own overhead"
    echo "# TYPE zram_memory_used_bytes gauge"
    echo "# HELP zram_memory_used_max_bytes High-water mark of that real RAM since boot"
    echo "# TYPE zram_memory_used_max_bytes gauge"
    echo "# HELP zram_compression_ratio Original bytes per byte of real RAM used"
    echo "# TYPE zram_compression_ratio gauge"
    emit
} > "$OUT.tmp" && mv -f "$OUT.tmp" "$OUT"
# Written to a temp file and moved into place: node_exporter reads this directory on its own
# schedule and would otherwise sometimes catch a half-written file and log a parse error.
