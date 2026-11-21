# Disk health: SMART and spin state

`disk-health` reads every `sd*` disk with `smartctl` once a minute and serves the result to
Prometheus. The **Disk Health** dashboard (Home, System) shows it: whether each disk is spinning,
the drive's own health verdict, temperature, spin-ups, and the counters that move before a disk
dies.

SMART (Self-Monitoring, Analysis and Reporting Technology) is the drive's own log of its wear and
errors. node-exporter sees I/O, not the drive, so none of this was visible before.

---

## Reading the dashboard

| Panel | What it tells you |
| :--- | :--- |
| Spinning / Spin state | `ACTIVE or IDLE` versus `STANDBY`, as the drive reports it |
| SMART health | The drive's overall self-assessment. It stays "Passed" until the disk is nearly gone, so do not wait for it |
| Failure counters | Raw counts that should stay at 0. **Reallocated** (5) and **pending** (197) sectors climbing is the classic early warning. **UDMA CRC errors** (199) point at the cable or the USB bridge, not the platters |
| Spin-ups per hour | `Start_Stop_Count` (4) going up. Many sleep/wake cycles an hour wear a desktop drive faster than spinning all day |
| SMART data age | How old the SMART values are; see below |

## It never wakes a disk

Every call is `smartctl -n standby`: a disk in standby answers that it is asleep and nothing else,
so monitoring does not spin it up. Its SMART values are then those of the last time it was awake,
which is what **SMART data age** shows. Old data there means the disk has been asleep, or smartctl
cannot reach it (`disk_health_poll_ok == 0`).

What the exporter does **not** do: put disks to sleep. Nothing on the host spins them down (no
`hd-idle`, no `hdparm -S`), and with the systemd journal and the app databases on the HDDs they
would be woken within seconds anyway. A disk that never sleeps is the expected state here.

## Why its own exporter

The community `smartctl_exporter` was the first choice and does the SMART half, but:

- it has no spin-state metric that works: with a disk in standby it skips the read and keeps
  serving the last cached values, with nothing saying the disk is asleep;
- it finds disks with `smartctl --scan`, which only looks in `/dev`, and mounting the host's `/dev`
  over the container's hides the container's own device nodes.

node-exporter's textfile collector is not an option either ([alerting.md](alerting.md#how-numbers-get-to-grafana)).

## Permissions

The container is not privileged. It gets exactly what `smartctl` needs to send ATA commands:

- `SYS_RAWIO`, the capability for raw SCSI pass-through, which is how SMART reaches a disk;
- a device cgroup rule `b 8:* rm`: read access to block devices of major 8, the `sd*` disks;
- the host's `/dev` read-only at `/host-dev`, where it looks for `sd[a-z]+`.

`-d sat` (SCSI-to-ATA translation) is the default device type because it covers both SATA disks
and USB enclosures. smartctl cannot auto-detect the type here: it maps a USB bridge from the
device's path, and the path is `/host-dev`, not `/dev`. A disk that needs another type (NVMe, a RAID
controller) can set `SMARTCTL_DEVICE_TYPE`, for every disk at once.

## Metrics

| Metric | Labels | Meaning |
| :--- | :--- | :--- |
| `disk_health_active` | disk | 1 spinning, 0 standby/sleep |
| `disk_health_poll_ok` | disk | 0 when smartctl got no answer from the disk |
| `disk_health_smart_passed` | disk | Overall health self-assessment |
| `disk_health_temperature_celsius` | disk | Current temperature |
| `disk_health_attribute_raw`, `_normalized`, `_threshold` | disk, id, name | Every SMART attribute. Some vendors pack several numbers into one raw value (Seagate's 1, 7, 195, 240), so only trust the raw value of counters |
| `disk_health_info` | disk, model, serial | Identity, to join on `disk` |
| `disk_health_smart_timestamp_seconds` | disk | When SMART was last read |
| `disk_health_smartctl_exit_status` | disk | smartctl's exit bitmask from the last awake poll |
| `disk_health_last_poll_timestamp_seconds` | | When the last poll of all disks finished |

`disk` is the kernel name (`sda`), the same label node-exporter uses, so the two join. It can change
across reboots when disks enumerate in another order; `disk_health_info` tells which drive it was.
