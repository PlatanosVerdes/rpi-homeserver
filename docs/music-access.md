# Music (ytify) — public access gated by Cloudflare Access

`ytify` (self-hosted, `config/ytify/`) streams audio straight from YouTube in the browser, so
there is no local music library to maintain, the same trait Acestream has for TV: nothing to
store, just a stream. That also means it does not need to live behind Tailscale-only access like
everything else — it is reachable from the open internet, gated by Google login, so it works from
a phone with no VPN connected.

```
Phone (no VPN) ──► https://music.platanosverdes.com
                         │  (Cloudflare edge: Access checks Google login first)
                         ▼
                  cloudflared container ──► localhost:443 (Caddy, same cert as everything else)
                                                │
                                                ▼
                                          ytify:80 (nginx, static build)
```

On the tailnet, `music.platanosverdes.com` still resolves straight to `TAILSCALE_IP` via Pi-hole
like every other service (see the main Networking section) and never touches the tunnel or
Cloudflare Access at all — being on the tailnet is already the trust boundary there, same as
Grafana, qBittorrent, etc. The Google gate only applies to the public path.

This reuses the **same** Cloudflare Tunnel already running for the deploy webhook
(see [deploy-webhook.md](deploy-webhook.md)) — one more Public Hostname on the same tunnel, not a
second tunnel.

## Setup (dashboard, one time)

### 1. Add Google as an identity provider

Cloudflare Access does not have a built-in "Login with Google" — you point it at your own Google
OAuth client:

1. [Google Cloud Console](https://console.cloud.google.com) → any project → **APIs & Services** →
   **Credentials** → **Create Credentials** → **OAuth client ID** → type **Web application**.
2. Authorized JavaScript origin: `https://<your-team-name>.cloudflareaccess.com`
3. Authorized redirect URI: `https://<your-team-name>.cloudflareaccess.com/cdn-cgi/access/callback`
4. Copy the **Client ID** and **Client secret** it gives you.
5. Cloudflare Zero Trust → **Settings** → **Authentication** → **Login methods** → **Add new** →
   **Google** → paste both values.

One-time per Cloudflare account — reused by every Access application you add later.

### 2. Add the public hostname to the existing tunnel

Zero Trust → **Networks** → **Tunnels** → the `rpi` tunnel (the one from deploy-webhook) →
**Public Hostnames** → **Add a public hostname**:

| Field | Value |
| :--- | :--- |
| Subdomain | `music` |
| Domain | `platanosverdes.com` |
| Service | `HTTPS` → `localhost:443` |

`localhost:443` is Caddy itself (published on the host, see `compose-core.yml`), so this hits
Caddy's own valid Let's Encrypt cert for `music.platanosverdes.com` — no "No TLS Verify" needed.

### 3. Gate it with Access

Zero Trust → **Access** → **Applications** → **Add an application** → **Self-hosted**:

| Field | Value |
| :--- | :--- |
| Application domain | `music.platanosverdes.com` |
| Identity providers | Google only (uncheck everything else) |
| Policy | Allow — Include → **Emails** → the specific address(es) allowed in |

Anyone else hitting the hostname gets Cloudflare's own login wall and never reaches Caddy or the
Pi at all.

### 4. Install it on a phone

`ytify` ships as a PWA (`vite-plugin-pwa`), so once `https://music.platanosverdes.com` loads (after
the Google login):

- **Android (Chrome):** menu → **Add to Home screen** / **Install app**.
- **iOS (Safari):** Share sheet → **Add to Home Screen**.

Either way it behaves like a native app (own icon, no browser chrome, offline support for anything
already played), no App Store involved.

## Notes

- `versions.env` pins `YTIFY_COMMIT` to a specific commit SHA, not a tag — upstream publishes no
  version tags/releases, same reasoning as the digest-pinned images (aceserve, etc.).
- `config/ytify/Dockerfile` clones `n-ce/ytify` at that pinned commit and builds it with `npm`;
  the result is a static site served by plain nginx, same pattern as Caddy's own Dockerfile
  (a from-source build, not custom code we wrote — that is what `services/` is for).
- No backend runs on the Pi for the actual streaming: once the static app is loaded, the browser
  talks directly to a public Piped/Invidious instance and to YouTube's CDN. The Pi only serves the
  static files.
