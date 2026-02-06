# 🏠 Raspberry Pi Home Server

Complete home server with Plex, Nextcloud, Pi-hole, Tailscale and automated download stack.

## 📦 Included Services

### 🔒 Network & Security

- **Tailscale** - VPN for secure remote access
- **Pi-hole** - Ad blocker and DNS

### 🎬 Media Server

- **Plex** - Movie and TV series server
- **Radarr** - Automatic movie management
- **Sonarr** - Automatic TV series management
- **Prowlarr** - Torrent indexer
- **qBittorrent** - Download client

### ☁️ Private Cloud

- **Nextcloud** - Your own Google Drive

### 📊 Monitoring

- **Grafana** - Visualization and dashboards
- **Prometheus** - Metrics collection and storage
- **Node Exporter** - System metrics (CPU, RAM, disk, network)
- **cAdvisor** - Docker container metrics
- **Pi-hole Exporter** - Pi-hole statistics
- **Exportarr** - Radarr/Sonarr/Prowlarr metrics
- **Tautulli** - Plex monitoring and statistics
- **SMARTCTL Exporter** - Disk health and RAID monitoring

## 🚀 Installation Guide

### Option A: Automatic Setup (Recommended)

#### 1️⃣ Clone and Configure

```bash
# Clone repository
git clone <your-repo>
cd rpi-homeserver

# Copy and edit configuration
cp .env.example .env
nano .env
```

**Configure these variables in `.env`:**

- `HOSTNAME` - Your server name (default: banana)
- `DOMAIN` - Local domain (default: banana.lan)
- `PLEX_CLAIM` - Get token at https://www.plex.tv/claim/
- `TAILSCALE_AUTHKEY` - Get key at https://login.tailscale.com/admin/settings/keys
- `PIHOLE_PASSWORD` - Your Pi-hole admin password
- `GRAFANA_PASSWORD` - Your Grafana admin password
- `DATA_PATH` - Where to store data (default: /mnt/data)

#### 2️⃣ Run Setup Script

```bash
# Make setup script executable
chmod +x scripts/setup.sh

# Run interactive setup
./scripts/setup.sh
```

Choose option **1** for full first-time installation.

The setup will:

- ✅ Install Docker
- ✅ Mount disks
- ✅ Create all directories
- ✅ Set correct permissions
- ✅ Start all services
- ✅ Configure DNS

#### 3️⃣ Post-Installation

```bash
# Setup Grafana dashboards
./scripts/setup.sh  # Choose option 4

# Get API keys for monitoring
./scripts/setup.sh  # Choose option 5
```

---

### Option B: Manual Setup (Step by Step)

If you prefer to run each step manually without the setup script:

#### Step 1: Clone and Configure

```bash
git clone <your-repo>
cd rpi-homeserver

# Copy and edit .env file
cp .env.example .env
nano .env
```

#### Step 2: Install Docker

```bash
chmod +x scripts/install_docker.sh
./scripts/install_docker.sh
```

This installs Docker, adds your user to docker group, and enables the service.

#### Step 3: Mount Disks (Optional - if using external disk)

```bash
chmod +x scripts/mount_disks.sh
./scripts/mount_disks.sh
```

Or manually mount your disk at `/mnt/data` (or the path in `DATA_PATH`).

#### Step 4: Create Directory Structure

```bash
# Data directories
sudo mkdir -p /mnt/data/media/{movies,series}
sudo mkdir -p /mnt/data/downloads/{complete,incomplete}
sudo mkdir -p /mnt/data/nextcloud

# Config directories
mkdir -p config/{plex,pihole/{etc-pihole,etc-dnsmasq},qbittorrent,prowlarr,radarr,sonarr,nextcloud,tailscale,grafana,prometheus,tautulli}
```

#### Step 5: Set Permissions

```bash
chmod +x scripts/permissions.sh
./scripts/permissions.sh
```

Or manually:

```bash
sudo chown -R $(id -u):$(id -g) /mnt/data
sudo chown -R $(id -u):$(id -g) config/
```

#### Step 6: Start Docker Services

```bash
docker compose up -d
```

Wait a few minutes for all services to start.

#### Step 7: Configure DNS (Optional but Recommended)

```bash
chmod +x scripts/setup_dns.sh
./scripts/setup_dns.sh
```

This creates local DNS entries for `banana.lan` domain.

#### Step 8: Setup Grafana Datasource

```bash
# Wait for Grafana to be ready
sleep 30

chmod +x scripts/setup_grafana.sh
./scripts/setup_grafana.sh
```

#### Step 9: Get API Keys for Monitoring

```bash
# Wait for *arr apps to be ready
sleep 60

chmod +x scripts/get_api_keys.sh
./scripts/get_api_keys.sh
```

Copy the API keys to your `.env` file and restart the exporters:

```bash
nano .env  # Add RADARR_API_KEY, SONARR_API_KEY, PROWLARR_API_KEY
docker compose restart exportarr-radarr exportarr-sonarr exportarr-prowlarr
```

#### Step 10: Configure Router DNS

Set your router's primary DNS to your Raspberry Pi IP address so all devices can use the local DNS names.

---

### Option C: RAID Setup (Optional - Data Protection)

If you want RAID1 (disk mirroring) for data protection, run this **before Step 4**:

```bash
chmod +x scripts/setup_raid.sh
chmod +x scripts/setup_raid_monitoring.sh

# Setup RAID1 array
sudo ./scripts/setup_raid.sh

# Setup monitoring
sudo ./scripts/setup_raid_monitoring.sh
```

This will create a RAID1 array mounted at `/mnt/data`.

## 🌐 Access Services

### Using Local DNS - 🍌 banana.lan (Recommended)

After running `./scripts/setup_dns.sh` and configuring your router to use Pi-hole as DNS:

| Service     | URL                                  | Port  |
| ----------- | ------------------------------------ | ----- |
| Plex        | `http://plex.banana.lan:32400/web`   | 32400 |
| Pi-hole     | `http://pihole.banana.lan/admin`     | 80    |
| Nextcloud   | `https://nextcloud.banana.lan:444`   | 444   |
| qBittorrent | `http://qbittorrent.banana.lan:8080` | 8080  |
| Radarr      | `http://radarr.banana.lan:7878`      | 7878  |
| Sonarr      | `http://sonarr.banana.lan:8989`      | 8989  |
| Prowlarr    | `http://prowlarr.banana.lan:9696`    | 9696  |
| Grafana     | `http://grafana.banana.lan:3000`     | 3000  |
| Tautulli    | `http://tautulli.banana.lan:8181`    | 8181  |
| Prometheus  | `http://prometheus.banana.lan:9090`  | 9090  |

### Using IP Address (Fallback)

| Service     | URL                        | Port  |
| ----------- | -------------------------- | ----- |
| Plex        | `http://your-ip:32400/web` | 32400 |
| Pi-hole     | `http://your-ip/admin`     | 80    |
| Nextcloud   | `https://your-ip:444`      | 444   |
| qBittorrent | `http://your-ip:8080`      | 8080  |
| Radarr      | `http://your-ip:7878`      | 7878  |
| Sonarr      | `http://your-ip:8989`      | 8989  |
| Prowlarr    | `http://your-ip:9696`      | 9696  |
| Grafana     | `http://your-ip:3000`      | 3000  |
| Tautulli    | `http://your-ip:8181`      | 8181  |
| Prometheus  | `http://your-ip:9090`      | 9090  |

### Using Tailscale (Remote Access)

When connected to your Tailscale network, access via: `http://banana:port`

## 📁 Folder Structure

```
/mnt/data/
├── media/
│   ├── movies/     # Movies for Plex
│   └── series/     # TV series for Plex
├── downloads/      # qBittorrent downloads
└── nextcloud/      # Nextcloud files

config/             # Persistent configurations
├── plex/
├── nextcloud/
├── pihole/
├── tailscale/
├── qbittorrent/
├── radarr/
├── sonarr/
└── prowlarr/
```

## 🔧 Additional Setup Options

The [setup.sh](scripts/setup.sh) script provides these options:

```
1) Full Setup              - Complete first-time installation
2) Setup DNS               - Configure banana.lan domain
3) Setup RAID              - Create RAID1 for data protection
4) Setup Grafana           - Configure monitoring dashboards
5) Get API Keys            - Extract keys from *arr apps
6) Check Container Health  - View service status
7) Fix Permissions         - Reset folder permissions
```

### Using the Setup Script

```bash
./scripts/setup.sh
```

Then select the option you need from the menu.

---

## 🎨 Service Configuration

After installation, configure each service:

### Plex

1. Access `http://plex.banana.lan:32400/web` (or `http://your-ip:32400/web`)
2. Sign in with your Plex account
3. Add libraries pointing to `/media/movies` and `/media/series`

### Pi-hole

1. Access `http://pihole.banana.lan/admin` (or `http://your-ip/admin`)
2. Use the password defined in `PIHOLE_PASSWORD`
3. Configure your router to use the Raspberry Pi IP as DNS
4. The DNS setup script already configured local domains

### Nextcloud

1. Access `https://nextcloud.banana.lan:444` (or `https://your-ip:444`)
2. Accept self-signed certificate
3. Create your admin user
4. Configure data folder as `/data`

### Tailscale

1. Check logs: `docker logs tailscale`
2. Your Raspberry will appear at https://login.tailscale.com/admin/machines
3. Access all services securely from anywhere

#### Configure Pi-hole for Tailscale Network (Ad-blocking for all Tailscale users)

To enable ad-blocking for ALL devices connected to your Tailscale network:

**Step 1: Configure Pi-hole to listen on Tailscale interface**

1. Access Pi-hole admin: `http://pihole.banana.lan/admin` (or `http://your-ip/admin`)
2. Go to **Settings** → **DNS**
3. In the upper right corner, toggle from **Basic** to **Expert**
4. Scroll to **Interface settings**
5. Check the box: **Permit all origins**

   ⚠️ **Security Note**: Only enable this if your Raspberry Pi is behind a firewall and you use a strong Pi-hole password.

**Step 2: Get your Raspberry Pi's Tailscale IP**

```bash
# SSH into your Raspberry Pi and run:
tailscale ip -4
```

This will show an IP like `100.64.x.x` or `100.x.x.x` - this is your **Tailscale IP** (NOT your router IP like 192.168.x.x or your public IP).

**Step 3: Configure Tailscale to use Pi-hole as DNS (Manual - via Tailscale Admin Panel)**

1. Go to https://login.tailscale.com/admin/dns
2. Under **Nameservers** section, click **Add nameserver**
3. Select **Custom**
4. Enter your Raspberry Pi's **Tailscale IP** from Step 2 (e.g., `100.64.x.x`)
5. Click **Save**
6. Enable the **Override local DNS** toggle

**Step 4: Disable key expiry for Raspberry Pi**

To keep your Raspberry Pi always connected without re-authentication:

1. Go to https://login.tailscale.com/admin/machines
2. Find your Raspberry Pi in the list
3. Click the **⋯** (three dots) menu next to it
4. Select **Disable key expiry**

⚠️ **Security Note**: Only do this for trusted devices. Revoke the key immediately if the device is lost or compromised.

**✅ Done! Now everyone who connects to your Tailscale network automatically gets:**

- 🛡️ Ad-blocking via Pi-hole
- 🎬 Access to Plex, Nextcloud, and all your services
- 🌍 Works from anywhere in the world

**Understanding IP Types:**

- **Router IP** (192.168.x.x): Local network only, assigned by your home router
- **Public IP**: Your ISP-assigned internet address (visible to websites)
- **Tailscale IP** (100.x.x.x): Virtual private network IP, only visible within your Tailscale network
- **Static IP** (configured in `.env`): Reservation in your router so RPi always gets the same local IP

**Note**: The nameserver configuration is done through Tailscale's web admin panel and cannot be automated from this project. It's a one-time manual setup per Tailscale account.

### Download Stack (Radarr/Sonarr)

1. Configure **Prowlarr** first (add indexers)
2. In Radarr/Sonarr, add Prowlarr and qBittorrent
3. Configure paths:
   - Movies: `/movies`
   - Series: `/series`
   - Downloads: `/downloads`

### Grafana - Monitoring Dashboard

1. Access `http://your-ip:3000`
2. Login with credentials from `.env` (default: admin / your_password)
3. Run setup script to configure datasource:
   ```bash
   ./scripts/setup_grafana.sh
   ```
4. Import recommended dashboards:
   - **Node Exporter Full** (ID: 1860) - System metrics
   - **Docker Container & Host** (ID: 179) - Container stats
   - **Raspberry Pi Monitoring** (ID: 10578) - RPi specific
   - **Pi-hole Exporter** (ID: 10176) - Pi-hole stats
   - **Exportarr** (ID: 15683) - Radarr/Sonarr/Prowlarr

**To import a dashboard:**

- Click `+` → `Import` → Enter dashboard ID → Select Prometheus datasource

**What you can monitor:**

- 📊 **System**: CPU, RAM, Disk, Network
- 🐳 **Docker**: All container stats and resource usage
- 🌡️ **Hardware**: Raspberry Pi temperature, voltage, throttling
- 💾 **RAID**: Disk health, SMART data, array status
- 🛡️ **Pi-hole**: Queries blocked, top domains, clients
- 🎬 **Plex**: Currently playing, bandwidth (via Tautulli)
- 📥 **Download Stack**: Radarr/Sonarr queue, health, stats
- 🔍 **Prowlarr**: Indexer health and stats
- 📈 **Historical data**: 30 days retention

### Tautulli - Plex Statistics

1. Access `http://your-ip:8181`
2. During first setup, point to Plex server: `http://plex:32400`
3. Login with your Plex account
4. Monitor currently playing, history, statistics

### Setup Monitoring for \*arr Apps

After starting all services, get the API keys:

```bash
# Extract API keys automatically
./scripts/get_api_keys.sh

# Or use the setup script menu option 5:
./scripts/setup.sh  # Choose: 5) Get API Keys

# Or get them manually from each app's web interface:
# Radarr: http://your-ip:7878/settings/general
# Sonarr: http://your-ip:8989/settings/general
# Prowlarr: http://your-ip:9696/settings/general
```

Add the API keys to [.env](.env):

```bash
RADARR_API_KEY=your_key_here
SONARR_API_KEY=your_key_here
PROWLARR_API_KEY=your_key_here
```

Restart the exporters:

```bash
docker compose restart exportarr-radarr exportarr-sonarr exportarr-prowlarr
```

- Movies: `/movies`
- Series: `/series`
- Downloads: `/downloads`

## 🛠️ Useful Commands

```bash
# View logs of all services
docker compose logs -f

# View logs of specific service
docker compose logs -f plex

# Check health status of all containers
./scripts/check_health.sh

# Restart a service
docker compose restart plex

# Stop all services
docker compose down

# Update images
docker compose pull
docker compose up -d
```

## 🔄 High Availability & Auto-Recovery

Your setup already includes **automatic recovery** without Kubernetes:

### ✅ What's Already Protecting You:

1. **Auto-restart on failure**: All services have `restart: unless-stopped`
2. **Health checks**: Docker monitors services and restarts unhealthy ones
3. **Grafana monitoring**: Real-time alerts if something goes wrong
4. **RAID1**: Protects against disk failure
5. **Persistent data**: All configs survive container restarts

### 📊 Monitor Container Health:

```bash
# Use the setup script
./scripts/setup.sh  # Choose: 6) Check Container Health

# Or check manually
docker ps

# Watch container status in real-time
watch docker ps

# View Docker events
docker events
```

### 🚨 What happens if a container crashes?

1. **Docker detects** the failure via health checks
2. **Automatically restarts** the container (up to 3 retries)
3. **Grafana alerts** you if it stays down
4. **Your data is safe** (persistent volumes)

### Why NOT Kubernetes for single RPi 4B (4GB)?

- ❌ **Overhead**: K8s needs ~1.5GB RAM just for control plane
- ❌ **Single node**: No real HA benefit (still single point of failure)
- ❌ **Complexity**: Much harder to maintain
- ✅ **Docker Compose + health checks**: Perfect for single node
- ✅ **Leaves ~3GB** for your actual services

**If you want real HA**, you'd need multiple Raspberry Pis with k3s (lightweight k8s), but that's overkill for a home server.

## 💾 Mount External Disk

### Option 1: Single Disk (No Redundancy)

If using a USB/SATA external disk:

```bash
# View available disks
lsblk

# Get disk UUID
sudo blkid

# Edit fstab for automatic mounting
sudo nano /etc/fstab

# Add line (replace UUID):
# UUID=your-uuid-here /mnt/data ext4 defaults,nofail 0 2

# Mount
sudo mount -a
```

### Option 2: RAID1 (Mirror) - ✅ Recommended to Prevent Data Loss

RAID1 creates a mirror copy of your data on two disks. If one fails, you lose nothing.

**Requirements:**

- 2 USB/SATA disks of the same size (or similar)
- Both disks will be formatted (you'll lose all content)

**Installation (Fully Automated):**

```bash
# Use the setup script
./scripts/setup.sh  # Choose: 3) Setup RAID

# Or run scripts manually
sudo ./scripts/setup_raid.sh
sudo ./scripts/setup_raid_monitoring.sh
```

The setup will guide you step by step:

1. Shows available disks
2. You select the 2 disks to use
3. Creates the RAID1 array automatically
4. Formats and mounts it at `/mnt/data`
5. Configures automatic mounting on boot
6. Sets up daily health checks

**Quick Health Check:**

```bash
# Simple status check
sudo raid-check

# Detailed RAID information
sudo mdadm --detail /dev/md0

# Real-time monitoring
watch cat /proc/mdstat

# Monitor synchronization
cat /proc/mdstat

# View detailed information
sudo mdadm --detail --scan

# If a disk fails, replace:
sudo mdadm --manage /dev/md0 --fail /dev/sdb
sudo mdadm --manage /dev/md0 --remove /dev/sdb
# (connect new disk)
sudo mdadm --manage /dev/md0 --add /dev/sdc
```

**RAID1 Advantages:**

- ✅ Protection against disk failure
- ✅ No data loss if a disk breaks
- ✅ Transparent to applications
- ⚠️ Capacity = size of smallest disk
- ⚠️ Requires 2 disks

## 🔄 Backup

Make regular backups of:

- `config/` folder (all configurations)
- `/mnt/data/nextcloud/` (your files)
- `.env` file (credentials)

## ⚠️ Troubleshooting

### Incorrect permissions

```bash
# Use the setup script
./scripts/setup.sh  # Choose: 7) Fix Permissions

# Or run manually
./scripts/permissions.sh
```

### Plex doesn't detect files

```bash
# Check permissions and owner
ls -la /mnt/data/media/
docker compose restart plex
```

### Pi-hole doesn't block ads

Make sure to configure your Raspberry Pi IP as primary DNS in your router.

## 📚 Resources

- [Plex Documentation](https://support.plex.tv/)
- [Nextcloud Documentation](https://docs.nextcloud.com/)
- [Pi-hole Documentation](https://docs.pi-hole.net/)
- [Tailscale Documentation](https://tailscale.com/kb/)

## 📝 License

MIT
