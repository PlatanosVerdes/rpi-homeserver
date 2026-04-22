# Vaultwarden — HTTPS Setup Guide

## Why HTTPS is Required

Vaultwarden (self-hosted Bitwarden) requires a **secure context**, which browsers and mobile apps only grant over HTTPS. Without it:
- The mobile Bitwarden app refuses to connect
- Browser extensions may show warnings
- The web vault works over HTTP only on `localhost`

## Architecture

We use a **private domain + Cloudflare DNS challenge** to get valid Let's Encrypt certificates without exposing your home IP publicly:

```
Mobile App / Browser
       │
  Tailscale VPN  (encrypted tunnel, IP never public)
       │
  Caddy (port 443, HTTPS)  ←── Let's Encrypt cert via Cloudflare DNS challenge
       │
  Vaultwarden (port 80, internal)
```

**Caddy renews certificates automatically** — you never touch them again.

---

## Step 1 — Buy a Domain

Register a `.com` domain at a registrar like Namecheap (~$6–10/yr). After purchase, you don't need to point it anywhere publicly — it's only used for the certificate.

---

## Step 2 — Set Up Cloudflare

1. Create a free account at [cloudflare.com](https://cloudflare.com)
2. Click **Add a site** → enter your domain → choose the **Free** plan
3. Cloudflare will give you two nameservers (e.g. `cesar.ns.cloudflare.com`)
4. In Namecheap: **Domain List** → your domain → **Nameservers** → **Custom DNS** → paste both Cloudflare nameservers
5. Wait for Cloudflare to confirm the domain is active (email notification, usually 15 min–2 hours)

---

## Step 3 — Create a Cloudflare API Token

In Cloudflare: **My Profile** → **API Tokens** → **Create Token** → use the **"Edit zone DNS"** template.

**Required settings:**
- Permissions: `Zone → DNS → Edit`
- Zone Resources: `Specific zone` → your domain

Copy the token — it's only shown once. Add it to your `.env`:

```env
CF_API_TOKEN=your_token_here
```

---

## Step 4 — Configure Pi-hole Local DNS

Since Vaultwarden is only accessible via Tailscale (not public internet), your devices need to resolve the domain to your **Tailscale IP** internally.

In Pi-hole: **Settings** → **Local DNS** → **DNS Records**, add one entry per subdomain:

| Domain | IP (Tailscale) |
| :--- | :--- |
| `vault.yourdomain.com` | `100.x.x.x` |
| `raspi.yourdomain.com` | `100.x.x.x` |
| `jellyfin.yourdomain.com` | `100.x.x.x` |
| *(repeat for all subdomains)* | |

> Your Tailscale IP starts with `100.` — find it with `tailscale status`.

---

## Step 5 — Deploy Caddy with Cloudflare Plugin

The standard `caddy` image doesn't include the Cloudflare DNS plugin. A custom image is built automatically from [config/caddy/Dockerfile](../config/caddy/Dockerfile).

```bash
docker compose up -d --build caddy
```

Caddy will request the certificate from Let's Encrypt on the first HTTPS connection. Check logs:

```bash
docker logs caddy -f
```

A successful certificate issuance looks like:
```
certificate obtained successfully
```

---

## Step 6 — First-Time Vaultwarden Setup

1. Open `https://vault.yourdomain.com` in your browser
2. Click **Create Account** — register your master email and password
3. Log in and verify everything works
4. **Signups are disabled by default** (`SIGNUPS_ALLOWED=false` in `.env`) — no one else can register

**Admin panel** (for managing users, checking logs):
```
https://vault.yourdomain.com/admin
```
Use `VAULTWARDEN_ADMIN_TOKEN` from `.env` as the password.

---

## Step 7 — Connect the Bitwarden Mobile App

1. Open the Bitwarden app
2. Go to **Settings** → **Server URL**
3. Enter `https://vault.yourdomain.com`
4. Log in with the account you created in Step 6

> The desktop browser extension follows the same steps under Settings → Server URL.

---

## Troubleshooting

**Certificate not issued / Caddy logs show DNS errors:**
- Verify `CF_API_TOKEN` in `.env` is correct
- Check Cloudflare shows the domain as Active
- Ensure Pi-hole DNS records point to your Tailscale IP

**Mobile app says "cannot connect to server":**
- Confirm your device is connected to Tailscale
- Test DNS: `nslookup vault.yourdomain.com` should return your Tailscale IP

**Caddy container fails to start:**
- Check `docker logs caddy` for configuration errors
- Validate the Caddyfile: `docker exec caddy caddy validate --config /etc/caddy/Caddyfile`
