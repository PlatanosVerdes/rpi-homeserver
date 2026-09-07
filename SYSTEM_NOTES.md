# 🛠️ Global Server Configuration (Outside Docker)

This document logs the changes made at the operating system level (Raspberry Pi) that are necessary for the environment to function correctly.

## 📝 1. Docker Log Limit (Global)
To prevent the SD card/Disk from filling up with infinite logs (especially from Prometheus and Prowlarr), the log limit is configured natively in the Docker engine.

**Modified file:** `/etc/docker/daemon.json`

**Content:**
```json
{
  "log-driver": "json-file",
  "log-opts": {
    "max-size": "10m",
    "max-file": "3"
  }
}
```
*Note: After applying this global setting, it is no longer necessary to include the `logging:` block in any `docker-compose.yml` file.*