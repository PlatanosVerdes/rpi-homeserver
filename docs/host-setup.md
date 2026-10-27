# Host setup

From a fresh Raspberry Pi OS to the first deploy: everything outside Docker that the Pi itself needs.
Run it once per machine. Everything after this is converged by the deploy ([deploy.md](deploy.md)).

## 1. Docker and Tailscale

```bash
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker $USER          # log out and back in
curl -fsSL https://tailscale.com/install.sh | sh
```

## 2. The repo and `.env`

```bash
git clone https://github.com/PlatanosVerdes/rpi-homeserver.git ~/rpi-homeserver
cd ~/rpi-homeserver
cp .env.example .env                   # fill in STATIC_IP, TAILSCALE_IP, PUID/PGID, DATA_ROOT, keys
```

`PUID`/`PGID` are `id -u` and `id -g`; every container runs as that user. On the two nodes here,
`.env` is not edited on the Pi: the deploy writes it from an encrypted copy in a private repo
([secrets.md](secrets.md)). A second node also sets `COMPOSE_PROFILES` and `COMPOSE_FILE`
([nodes.md](nodes.md)).

## 3. External disk

`DATA_ROOT` is the data disk (here a mergerfs pool of two USB disks at `/mnt/data`). Mount it by UUID:

```bash
lsblk -f                               # UUID and fstype
sudo mkdir -p /mnt/data
echo 'UUID=your-disk-uuid  /mnt/data  ext4  defaults,noatime,nofail  0  2' | sudo tee -a /etc/fstab
sudo mount -a
```

`nofail` keeps a boot from hanging when the disk is unplugged. Set `DATA_ROOT=/mnt/data` and
`DATA_DB_ROOT=/mnt/data/db`: databases that write constantly (VictoriaLogs) go there, not
on the SD card.

## 4. Docker log limits

`/etc/docker/daemon.json`, so no compose file needs a `logging:` block:

```json
{ "log-driver": "json-file", "log-opts": { "max-size": "10m", "max-file": "3" } }
```

```bash
sudo systemctl restart docker
```

This only bounds how far back `docker logs` reaches; VictoriaLogs keeps 30 days ([logging.md](logging.md)).

## 5. The systemd journal

Debian ships `journald.conf` commented out, which caps the journal at 10% of the filesystem (about
11 GB on a 117 GB card), and Raspberry Pi OS keeps it in RAM, so a reboot erases why it rebooted.
One drop-in fixes both: on disk, three days, 200 MB.

`/etc/systemd/journald.conf.d/99-persistent-3d.conf`:

```ini
[Journal]
Storage=persistent
MaxRetentionSec=3day
MaxFileSec=1day
SystemMaxUse=200M
SystemMaxFileSize=25M
Compress=yes
```

```bash
sudo systemctl restart systemd-journald
sudo journalctl --flush        # journald keeps writing to RAM until it is told to migrate
```

Two things make this fail silently:

- **The `99-` prefix is load-bearing.** The OS ships `/usr/lib/systemd/journald.conf.d/40-rpi-volatile-storage.conf`
  with `Storage=volatile`, and drop-ins apply in filename order across every directory, so anything
  numbered below `40` is overridden. `/etc` only beats `/usr` for the *same* filename.
- **`MaxFileSec=1day` is what makes the 3 days real.** `MaxRetentionSec` only drops *rotated* files,
  so without daily rotation the active file keeps entries well past the window.

Verify with `systemd-analyze cat-config systemd/journald.conf | grep Storage` (last value wins).
Once it works, `journalctl --list-boots` lists more than the current boot and `journalctl -b -1`
reaches the log that ends at a crash.

## 6. IP forwarding, static IP, Tailscale

- **IP forwarding** and the **Tailscale** flags (`up` once, then only `set`): [tailscale.md](tailscale.md).
  Do this before anything relies on the tailnet; `--accept-dns=false` is what keeps Pi-hole working.
- **Static IP:** reserve the Pi's address in the router's DHCP settings against its MAC, for wifi
  **and** cable, and put it in `STATIC_IP`. The Tailscale `/32` routes and Plex depend on those
  addresses not moving.

## 7. Directories the containers expect

```bash
# node-exporter mounts it; nothing writes .prom files any more, but a missing one is created as root
sudo mkdir -p /var/lib/node_exporter/textfile_collector
sudo chown -R $USER:$USER /var/lib/node_exporter
```

`appdata/` bind mounts for services running as `1000:1000` are created and chowned by the deploy.
After restoring or copying `appdata` by hand, run `sudo chown -R $USER:$USER ~/rpi-homeserver/appdata`.

## 8. First start and cron

```bash
export COMPOSE_ENV_FILES=versions.env,.env    # image versions live in versions.env
docker compose up -d
bash scripts/setup/install-crontab.sh          # once; every deploy reinstalls it after this
```

The crontab comes from `scripts/crontab` (plus the companion repo's fragment), with `@PROJECT_DIR@`
replaced by this checkout and `# when:` jobs dropped where their service does not run. `crontab -e`
changes are overwritten. logrotate (`config/logrotate/`) and the udev readahead rules
(`config/udev/`) are installed by the deploy the same way; logrotate needs `copytruncate` because
cron holds the logs open with `>>`, and the policy must be root-owned 0644 or logrotate ignores it.

For deploys on push rather than every 30 minutes, set up the webhook and tunnel: [deploy.md](deploy.md).

## 9. LAN short names

Pi-hole answers `*.platanosverdes.com` for the tailnet on its own (written by the deploy from the
Caddyfile); those go through Tailscale. At home use `http://<name>.pi` (`http://jellyfin.pi`): the
deploy writes one per `http://<name>` site in Caddy, pointing at `STATIC_IP`, so it stays on the
LAN and the Pi does not encrypt the video twice. For bare `http://jellyfin` names, `/etc/hosts`:

```
<STATIC_IP>    raspi homepage jellyfin seerr plex grafana prometheus push prowlarr radarr sonarr flare torrent pihole
```

## 10. What still needs a human

```bash
bash scripts/recovery/recovery-status.sh
```

The deploy converges everything derivable from git and `.env`: formats and profiles in the arrs,
Pi-hole records, qBittorrent's limits, the Seerr and Bazarr links, Plex's LAN networks. This lists
what it cannot: the Plex claim, Jellyfin's setup wizard, and whether a fresh Grafana database came
up in WAL mode. After a restore from backup, see also [backups.md](backups.md).
