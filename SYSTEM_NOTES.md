# System Notes — Host-Level Configuration

Everything outside Docker that must be configured on the Raspberry Pi OS itself. Run these once during initial setup or after a fresh OS install.

---

## 1. Docker Log Limits

Prevents the SD card / disk from filling up with container logs (especially Prometheus and Prowlarr).

**File:** `/etc/docker/daemon.json`

```json
{
  "log-driver": "json-file",
  "log-opts": {
    "max-size": "10m",
    "max-file": "3"
  }
}
```

```bash
sudo systemctl restart docker
```

After applying this, no individual `compose-*.yml` file needs a `logging:` block.

---

## 2. External Disk Mount

The `DATA_ROOT` variable in `.env` points to the external disk. Mount it permanently by UUID so it survives reboots.

```bash
lsblk -f                          # find UUID and fstype (e.g. ext4)
sudo mkdir -p /mnt/data
```

Add to `/etc/fstab`:
```
UUID=your-disk-uuid  /mnt/data  ext4  defaults,noatime,nofail  0  2
```

`nofail` prevents a boot hang if the disk is unplugged.

```bash
sudo mount -a                     # apply without rebooting
```

---

## 3. IP Forwarding (for Tailscale exit node)

Required for the Pi to route traffic on behalf of Tailscale devices.

```bash
echo 'net.ipv4.ip_forward = 1' | sudo tee -a /etc/sysctl.d/99-tailscale.conf
echo 'net.ipv6.conf.all.forwarding = 1' | sudo tee -a /etc/sysctl.d/99-tailscale.conf
sudo sysctl -p /etc/sysctl.d/99-tailscale.conf
```

---

## 4. node_exporter Textfile Collector

Required for `tailscale-metrics` to push custom `.prom` files.

```bash
sudo mkdir -p /var/lib/node_exporter/textfile_collector
sudo chown -R $USER:$USER /var/lib/node_exporter
```

The `node-exporter` container in `compose-mon.yml` mounts this directory. `tailscale-metrics` writes to it via the host cron.

---

## 5. Cron Jobs

```bash
crontab -e
```

Active cron entries:

```
# Auto-deploy: pull git + docker compose up every 15 min
*/15 * * * * /home/raspi/rpi-homeserver/scripts/apply.sh >> /home/raspi/rpi-homeserver/apply.log 2>&1

# Tailscale metrics: write .prom file every minute for node_exporter
* * * * * /home/raspi/rpi-homeserver/services/tailscale-metrics/tailscale-metrics >> /home/raspi/rpi-homeserver/tailscale-metrics.log 2>&1
```

> The `tailscale-metrics` binary must be compiled first: `cd services/tailscale-metrics && make build`

---

## 6. Tailscale

```bash
# Install
curl -fsSL https://tailscale.com/install.sh | sh

# Exit node only
sudo tailscale up --advertise-exit-node --accept-dns=false

# Exit node + subnet routing (exposes local LAN 192.168.1.x to Tailscale devices)
sudo tailscale up \
  --advertise-exit-node \
  --advertise-routes=192.168.1.0/24 \
  --accept-dns=false
```

`--accept-dns=false` is critical — prevents Tailscale from overwriting `/etc/resolv.conf` and breaking Pi-hole + Docker DNS.

Then in [Tailscale admin console](https://login.tailscale.com/admin):
1. Machines → your Pi → Edit route settings → enable exit node
2. DNS tab → Global Nameservers → custom → Pi's Tailscale IP → Override local DNS

→ Full guide: [docs/tailscale.md](docs/tailscale.md)
→ Reference: https://tailscale.com/docs/solutions/block-ads-all-devices-anywhere-using-raspberry-pi

---

## 7. Static IP

Reserve a static local IP for the Pi in your router's DHCP settings (bind to Pi's MAC address). Set `STATIC_IP` in `.env` to match.

---

## 8. User Permissions

All containers run with `PUID`/`PGID` from `.env`. Get your values:

```bash
id -u   # → PUID
id -g   # → PGID
```

Ensure the user owns all `appdata/` and `config/` directories:

```bash
sudo chown -R $USER:$USER ~/rpi-homeserver/appdata ~/rpi-homeserver/config
```

## qBittorrent queue limits

`MaxActiveTorrents` and `MaxActiveUploads` raised from 18/15 to **40/40** on 2026-08-05, applied
live through the WebUI API (`/api/v2/app/setPreferences`).

Why: with 36 torrents and 18 active slots, half of them sat in `queuedUP`, which is **not seeding**.
Since seeding time only accumulates while active, the 5-day share limit set on each torrent took
roughly twice as long to reach, so torrents took twice as long to retire themselves. Right after the
change the queue went from 18 waiting to 0.

The limit is 40 rather than unlimited so a much larger library cannot open hundreds of connections on
a Pi. This setting lives in `appdata/qbittorrent/` and is therefore **not** reproducible from git:
if the Pi is rebuilt, set it again.
