"""Host numbers node_exporter does not have: what zram really costs, and where the disk went.

Both used to be cron scripts writing into node_exporter's textfile collector. They are here because
a file on disk cannot say "the thing that writes me is dead", and a scrape can.

The disk walk answers two questions in one pass, which is half the I/O the two `find` and `du` runs
cost: the largest files, and the size of each top-level folder. An inode is counted once in both,
because the arrs import by hardlink and the same bytes carry two names.
"""

import os
import time

DATA_ROOT = os.environ.get("DATA_ROOT", "/mnt/data")
TOP_FILES = int(os.environ.get("TOP_FILES", "30"))


def escape(value):
    return str(value).replace("\\", "\\\\").replace('"', '\\"').replace("\n", " ")


def zram():
    """/sys/block/zram*/mm_stat, whose columns are, in order: orig_data_size compr_data_size
    mem_used_total mem_limit mem_used_max same_pages pages_compacted huge_pages.

    node_exporter reports swap the same way whether it lives on a disk or in compressed RAM, so its
    SWAP gauge sits red while nothing is wrong. The number it cannot give is mem_used_total: the
    real RAM those compressed pages occupy.
    """
    lines = [
        "# HELP zram_original_bytes Uncompressed size of the data held in zram",
        "# TYPE zram_original_bytes gauge",
        "# HELP zram_compressed_bytes Size of that data after compression",
        "# TYPE zram_compressed_bytes gauge",
        "# HELP zram_memory_used_bytes Real RAM zram occupies, including its own overhead",
        "# TYPE zram_memory_used_bytes gauge",
        "# HELP zram_memory_used_max_bytes High-water mark of that real RAM since boot",
        "# TYPE zram_memory_used_max_bytes gauge",
        "# HELP zram_compression_ratio Original bytes per byte of real RAM used",
        "# TYPE zram_compression_ratio gauge",
    ]
    for name in sorted(os.listdir("/sys/block")):
        if not name.startswith("zram"):
            continue
        try:
            with open(f"/sys/block/{name}/mm_stat") as handle:
                fields = handle.read().split()
        except OSError:
            continue
        if len(fields) < 5:
            continue
        original, compressed, used, _limit, used_max = (int(x) for x in fields[:5])
        label = f'{{device="{name}"}}'
        lines += [
            f"zram_original_bytes{label} {original}",
            f"zram_compressed_bytes{label} {compressed}",
            f"zram_memory_used_bytes{label} {used}",
            f"zram_memory_used_max_bytes{label} {used_max}",
            # Derived here rather than on the panel: dividing by zero on an empty device would
            # render as NaN on a panel that exists to be reassuring.
            f"zram_compression_ratio{label} {original / used:.3f}" if used
            else f"zram_compression_ratio{label} 1",
        ]
    return lines


# Which top-level folder a hardlinked file is charged to. Bytes shared between a download and its
# import belong to both, so somebody has to be charged and `du` charged whichever it happened to
# walk first. The library goes first here on purpose: the film is what is being kept, the download
# name is temporary, and a panel answering "where did the disk go" that shows films as small
# because its bytes were charged to downloads is answering the wrong question. Either way the
# folders add up to the same disk.
FIRST = ("films", "series")


def walk(root):
    """(inode, size, path) for every file under root, following no symlinks. Top-level folders are
    visited in FIRST order, which decides who is charged for a shared inode.
    """
    try:
        tops = sorted(entry.path for entry in os.scandir(root) if entry.is_dir())
    except OSError:
        return
    stack = [path for path in tops if os.path.basename(path) not in FIRST]
    stack += [os.path.join(root, name) for name in reversed(FIRST)
              if os.path.join(root, name) in tops]
    while stack:
        current = stack.pop()
        try:
            entries = list(os.scandir(current))
        except OSError:
            continue
        for entry in entries:
            try:
                if entry.is_dir(follow_symlinks=False):
                    stack.append(entry.path)
                elif entry.is_file(follow_symlinks=False):
                    info = entry.stat(follow_symlinks=False)
                    yield info.st_ino, info.st_size, entry.path
            except OSError:
                continue


def disk():
    biggest = {}          # inode -> (size, path)
    try:
        per_root = {entry.name: 0 for entry in os.scandir(DATA_ROOT) if entry.is_dir()}
    except OSError:
        per_root = {}
    for inode, size, path in walk(DATA_ROOT):
        if inode in biggest:
            # Same bytes under a second name. The later path alphabetically wins, which is films/
            # or series/ over downloads/: the same number either way, and the library path is the
            # one worth reading on a panel.
            if path > biggest[inode][1]:
                biggest[inode] = (size, path)
            continue
        biggest[inode] = (size, path)
        # Charged to the folder that got there first, which FIRST above decides.
        top = os.path.relpath(path, DATA_ROOT).split(os.sep)[0]
        per_root[top] = per_root.get(top, 0) + size

    lines = ["# HELP disk_file_bytes Size in bytes of the largest individual files on the data disk.",
             "# TYPE disk_file_bytes gauge"]
    ranked = sorted(biggest.values(), key=lambda item: item[0], reverse=True)[:TOP_FILES]
    for size, path in ranked:
        top = os.path.relpath(path, DATA_ROOT).split(os.sep)[0]
        lines.append(f'disk_file_bytes{{root="{escape(top)}",path="{escape(path)}"}} {size}')

    lines += ["# HELP disk_root_bytes Size in bytes of each top-level folder on the data disk.",
              "# TYPE disk_root_bytes gauge"]
    for top, size in sorted(per_root.items(), key=lambda item: item[1], reverse=True):
        lines.append(f'disk_root_bytes{{root="{escape(top)}"}} {size}')

    lines += ["# HELP disk_usage_scrape_timestamp_seconds Unix time this walk finished.",
              "# TYPE disk_usage_scrape_timestamp_seconds gauge",
              f"disk_usage_scrape_timestamp_seconds {int(time.time())}"]
    return lines


if __name__ == "__main__":
    print("\n".join(zram() + disk()))
