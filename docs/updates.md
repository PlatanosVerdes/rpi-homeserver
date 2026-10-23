# Image updates

[Renovate](https://docs.renovatebot.com/) watches every third-party image and base image this repo
pins and opens a pull request when a new version is out. Merging it is the deploy, like any other
commit to `main`. Config: [`renovate.json`](../renovate.json).

## What it watches

| Where | How Renovate finds it |
| :--- | :--- |
| `versions.env` | A `# renovate: image=<image>` line right above each version or digest |
| `services/*/Dockerfile` | `FROM` lines with a literal tag (the `golang` builders) |
| `.github/workflows/` | Actions pinned by commit SHA, with the version in the trailing comment |

Compose files are not read: every `image:` there is a `${VARIABLE}`, which Renovate cannot resolve,
so `versions.env` carries the image name itself. A version line without the annotation is ignored.
That is on purpose for the apps with their own repos (bifrost, sleipnir, muninn), which their
releases pin through "Deploy app", and for the `*_IMAGE_TAG` lines, which the Service images
workflow writes. The ffmpeg and python versions for audio-convert are Alpine packages, not images,
and stay manual.

Images without version tags (aceserve, the qBittorrent and Pi-hole exporters) are pinned by the
digest of `latest`, and Renovate proposes the new digest when `latest` moves.

linuxserver tags (`1.43.3.10861-07dfddaeb-ls318`, `10.11.11ubu2604-ls43`, `5.2.3_v2.0.13-ls469`)
get their own versioning rule, so an `-ls` rebuild counts as an update and an Ubuntu base change
(`ubu2604`) never does.

## When it runs and what it merges

- Monday before 07:00 Madrid time, and only versions published at least three days ago: a broken
  release usually gets pulled or patched inside that window. GHCR and lscr.io publish no release
  dates, so their images (the linuxserver ones, Seerr, Homepage, the *arrs' helpers) skip the wait;
  requiring a date would hold them back forever.
- **Merged on its own once CI passes:** minor, patch and digest updates to the monitoring stack
  (Prometheus and its exporters, cAdvisor, Vector, VictoriaLogs), grouped in one PR, and to GitHub
  Actions. A bad one there loses metrics or a CI run, nothing at home.
- **Everything else waits for a review.** Pi-hole takes DNS down for the whole tailnet, a Jellyfin
  or *arr major migrates its database with no way back, and Caddy, cloudflared and gluetun carry
  every route, the tunnel and the VPN. Read the release notes before merging those.
- The Dependency Dashboard issue lists every pending update, including what is waiting for the
  schedule or the three days.

Its pull requests run the same checks as anyone's: Host ports, and Service images, which builds
Caddy or audio-convert when a base version they take as a build arg changes.

## Held back

- **Jellyfin stays on 10.x.** 12.0 moved the server to .NET 10, so every non-bundled plugin needs a
  rebuild, and the SSO plugin behind the Google login ([9p4/jellyfin-plugin-sso](https://github.com/9p4/jellyfin-plugin-sso))
  has none: its last release targets 10.11. Upgrading would leave the Google button broken. 12 also
  migrates the database with no way back, so its notes ask for plugins removed and a backup first.
  To lift the hold, drop the `allowedVersions` rule in `renovate.json` once the plugin ships a 12
  build, back up `appdata/jellyfin` on both nodes, and run a full library scan after the upgrade.

## Adding a version line

Put the annotation right above the line, with the image as compose names it:

```bash
# renovate: image=filebrowser/filebrowser
FILEBROWSER_VERSION=v2.32.0
```

Nothing goes between the two lines: the match expects them adjacent. A comment for people goes
above the annotation.
