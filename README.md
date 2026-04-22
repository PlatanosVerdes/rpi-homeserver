## Infrastructure Overview
The system is split into specialized modules using Docker Compose's include feature for better maintainability:


# 🏠 RPi HomeServer: All-in-One Media, Monitoring Stack & Application server

A modular, Docker-based infrastructure for a Raspberry Pi home server. This setup uses Docker Compose's `include` feature to manage media, automation, monitoring, and custom bots in separate, maintainable modules.

---

## 📊 Infrastructure Overview

The stack is organized into the following modules:

| Module | Purpose | Key Services |
| :--- | :--- | :--- |
| **Core** | Entry point & Networking | Caddy, Homepage, Pi-hole, Speedtest-tracker |
| **Media** | Streaming & Live TV | Plex, Jellyfin, Aceserve (Acestream) |
| **Arrs** | Automation & Downloads | Radarr, Sonarr, Prowlarr, qBittorrent, FlareSolverr |
| **Monitoring** | System Health | Prometheus, Grafana, Pushgateway |
| **Apps** | Custom Bots | Pol Academy Offers Bot, Acestream Updater |

---

## 🚀 Installation & Deployment

### Clone the repository:
   ```bash
   git clone <your-repo-url> rpi-homeserver
   cd rpi-homeserver
   ```

### Project Structure:
- `config/`: Static configuration files (Caddyfile, Prometheus config, etc.)
- `appdata/`: Persistent data for containers (Databases, app configs).
- `scripts/`: Custom logic scripts like the Acestream updater.

### External Storage Setup (Mounting DATA_ROOT)

To ensure your external hard drive is always available at `/mnt/data` (your `DATA_ROOT`), follow these steps to mount it by its UUID.

1. **Identify your Drive**
Plug in your drive and find its partition UUID and File System type (usually `ext4` or `ntfs`):
```bash
lsblk -f
```
> Take note of the UUID and FSTYPE for the partition you want to use.
2. **Create the Mount Point**
```bash
sudo mkdir -p /mnt/data
sudo chown -R $USER:$USER /mnt/data
```
3. **Configure Permanent Mount (fstab)**
Edit the system's drive table:
```bash
sudo nano /etc/fstab
```
Add this line at the end of the file (replace with your actual UUID and FSTYPE):
```txt
UUID=your-uuid-here  /mnt/data  ext4  defaults,noatime,nofail  0  2
```
> Note: If using NTFS, use ntfs-3g instead of ext4.

4. **Mount and Verify**
```bash
sudo mount -a
df -h | grep /mnt/data
```

### Configure Environment:
Create a `.env` file based on your variables. Ensure you set your `PUID`, `PGID`, and `DATA_ROOT`.

### Deploy the stack:
```bash
docker compose up -d
```

---

## 🛠️ Manual Configuration Guide

### Tailscale Setup (Exit Node & Pi-hole)
To configure the Raspberry Pi as an Exit Node while maintaining local DNS resolution for Docker and Pi-hole:

1. **Static IP**
Assign a static IP to your machine from your router

2. **Enable IP Forwarding**
Allow the system to route traffic:
```bash
echo 'net.ipv4.ip_forward = 1' | sudo tee -a /etc/sysctl.d/99-tailscale.conf
echo 'net.ipv6.conf.all.forwarding = 1' | sudo tee -a /etc/sysctl.d/99-tailscale.conf
sudo sysctl -p /etc/sysctl.d/99-tailscale.conf
```

3. **Start Tailscale**
If you also want to expose your local LAN to the Tailscale network, use:
```bash
sudo tailscale up --advertise-exit-node --advertise-routes=IP-RANGE --accept-lan=true --accept-dns=false
```
   - `--advertise-routes=192.168.0.0/24`: Makes the local LAN accessible to other Tailscale devices.
   - `--accept-lan=true`: Allows the server to keep accessing devices on the local LAN while connected through Tailscale.
     
Run the following command to advertise the Exit Node without overriding local DNS settings:
```bash
sudo tailscale up --advertise-exit-node --accept-dns=false
```
   - `--advertise-exit-node`: Allows other devices to route their internet traffic through this server.
   - `--accept-dns=false`: **Critical**. Prevents Tailscale from overwriting `/etc/resolv.conf.` This ensures that Pi-hole and Docker containers can still resolve internal and external IPs correctly.

4. **Tailscale Admin Console (Web)**
   - **Login Admin Console:** https://login.tailscale.com/admin
   - **Enable Exit Node:** Go to _Machines_ -> `your-pi` -> _Edit route settings_ -> Enable "_Use as exit node_".

   - **Set Pi-hole as DNS:**
      - Go to the _DNS_ tab.
      - Under _Global Nameservers_, click _Add nameserver_ -> _Custom_.
      - Enter the Tailscale IP address of your Pi (starts with `100.x.y.z`).
      - Enable "_Override local DNS_".

   - **Pi-hole Configuration:**
   Since Tailscale requests come from a different subnet (`100.x.y.z`), you must allow them in Pi-hole:
      - Go to _Settings_ -> _DNS_ -> _Interface Settings_.
      - Select "_Permit all origins_".
      
> Note: More documentation here: https://tailscale.com/docs/solutions/block-ads-all-devices-anywhere-using-raspberry-pi 


### Caddy Reverse Proxy (Friendly URLs)

Caddy runs on port 80 and lets you access every service by a short name instead of remembering ports.

| Short URL | Service |
| :--- | :--- |
| `http://raspi` | Homepage dashboard |
| `http://jellyfin` | Jellyfin |
| `http://plex` | Plex |
| `http://overseerr` | Overseerr |
| `http://radarr` | Radarr |
| `http://sonarr` | Sonarr |
| `http://prowlarr` | Prowlarr |
| `http://torrent` | qBittorrent |
| `http://grafana` | Grafana |
| `http://prometheus` | Prometheus |
| `http://speedtest` | Speedtest Tracker |
| `http://pihole` | Pi-hole |

To make these names resolve, add the following entries to the `hosts` file on **each device** you use:

**Mac / Linux** → `/etc/hosts`  
**Windows** → `C:\Windows\System32\drivers\etc\hosts`

```
# From LAN
<STATIC_IP> raspi homepage jellyfin overseerr plex grafana prometheus push prowlarr radarr sonarr flare torrent speedtest pihole

# From Tailscale (add on devices that connect via Tailscale)
<TAILSCALE_IP>  raspi homepage jellyfin overseerr plex grafana prometheus push prowlarr radarr sonarr flare torrent speedtest pihole
```

> Note: You only need one of the two lines depending on how you connect. If you connect from both LAN and Tailscale, add both.

### Networking & Ad-Blocking (Pi-hole)
- Access: `http://<IP>:8081/admin`
- Setup: Configure your router's DHCP/DNS to point to your Raspberry Pi's IP.
> Note: The API password is set via `PIHOLE_PASSWORD` in your `.env`.

### Media Automation (The *arrs Suite)
To link everything correctly, follow these steps:

- **Prowlarr & FlareSolverr:**
1. In Prowlarr, go to _Settings -> Indexers -> Add Proxy._
2. Add FlareSolverr using `http://flaresolverr:8191`.
3. This allows Prowlarr to bypass DDoS protection on trackers.

- **Prowlarr to Radarr/Sonarr**:
1. In Prowlarr, go to Settings -> Apps.
2. Add Radarr (`http://radarr:7878`) and Sonarr (`http://sonarr:8989`) using their respective API Keys.
- **Download Client:**
1. In Radarr/Sonarr, add qBittorrent under _Settings -> Download_ Clients.
2. Host: `qbittorrent`, Port: `8080`.

### Acestream Live TV Integration
- **How it works:** The `acestream-updater` container runs a background loop that fetches IPFS channels, converts them for aceserve, and saves a `.m3u` file.

- **Jellyfin Setup:** 
1. Open Jellyfin Dashboard -> _Live TV._
2. Add a _Tuner Setup (M3U)_.
3. File path: `/data/channels_ace.m3u` (mapped from your appdata).

### Auto-Deployment (deploy_control.sh)
The script `scripts/deploy_control.sh` runs automatically every 15 minutes via cron. It pulls the latest changes from git and only rebuilds Docker images when there are actual changes — avoiding unnecessary work on every run.

- **Cron entry** (already configured on the Pi):
```bash
*/15 * * * * /home/raspi/rpi-homeserver/scripts/deploy_control.sh > /home/raspi/rpi-homeserver/deploy_control.log 2>&1
```
- **Logs:** `deploy_control.log` in the project root.
- **Metrics:** Pushes run stats to Pushgateway → visible in the **Deploy Monitor** dashboard in Grafana.

### Monitoring (Prometheus & Grafana)
- **Grafana:** `http://<IP>:3000` (Default: admin/admin).
- **Dashboards** are auto-provisioned from `config/grafana/dashboards_json/` under the **Scripts** folder:
  - **Acestream Monitor** — channel sync executions, changes, errors.
  - **Deploy Monitor** — deploy runs, changes applied, errors.
- **System dashboards** (import by ID in Grafana → Dashboards → Import):
  - `1860` — Node Exporter Full (CPU, RAM, disk, network)
  - `193` — cAdvisor (per-container metrics)
- **If a dashboard shows "No Data"**, trigger the relevant script manually:
```bash
docker exec -it acestream-updater bash /app/script.sh   # acestream
bash /home/raspi/rpi-homeserver/scripts/deploy_control.sh  # deploy
```

### Dashboard (Homepage)
The Homepage service (`port 3001`) acts as your central hub. You must manually add your API Keys for Radarr, Sonarr, Plex, etc., into the `.env` file for the widgets to display real-time data.

## 📝 Global System Notes (Host Level)
These configurations are applied directly to the Raspberry Pi OS to ensure stability.

### Docker Log Management
To prevent the SD card from filling up, we limit log sizes globally in `/etc/docker/daemon.json`:
```json
{
  "log-driver": "json-file",
  "log-opts": {
    "max-size": "10m",
    "max-file": "3"
  }
}
```
