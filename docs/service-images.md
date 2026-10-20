# Service images: built by CI, pulled by the Pi

Every service compose builds from a directory in `services/` (Caddy with its Cloudflare plugin, and
the Go services) is built by GitHub Actions and published to GHCR, GitHub's container registry, as
`ghcr.io/platanosverdes/rpi-homeserver-<service>`. The Pi pulls those images and, in the normal path,
compiles nothing: an ARM Go build is about five minutes of the Pi's CPU, spent on the same machine
that is serving everything else.

The workflow is `.github/workflows/service-images.yml`; the decisions it makes live in
`scripts/ci/service-images.py` so they can be run and read outside Actions.

---

## The path of a change

```
push to main touching services/caddy/
   │
   ├─ webhook ──► apply.sh on the Pi: nothing to do yet, Caddy keeps its pinned image
   │
   └─ Service images workflow
        plan ........ which services are due, and their build args from versions.env
        build ....... one job per service, native arm64 runner, push to GHCR
        pin ......... CADDY_IMAGE_TAG=v2026.10.20-1a2b3c4 in versions.env
                      commit "Deploy caddy v2026.10.20-1a2b3c4"
                            │
                            ▼
                      webhook ──► apply.sh: pulls the new tag, recreates caddy and nothing else
```

The pushed source does not deploy that service by itself. The commit that changes the pin does, a
few minutes later, which is the same model "Deploy app" (`bump-app-versions.yml`) uses for the
apps that live in their own repos: the image exists before anything asks the Pi to run it.

The tag is `v<commit date>-<short commit>`. A tag is never pushed twice: a re-run that finds its tag
already on GHCR skips the build, so what a pin names cannot change under it.

---

## What gets built, and when

`plan` reads `docker compose config`, so the list of services is whatever compose builds from this
repo. Nothing to register: a new directory with a Dockerfile and a compose entry is picked up.

A service is due when, since **the commit its pinned tag was built from**:

- a file under its build context changed (the Dockerfile, the Go source, its `.dockerignore`), or
- its resolved build config changed: the Dockerfile path or a build arg. Build args come from
  `versions.env` through compose (`CADDY_VERSION` for Caddy), so a bump there rebuilds exactly the
  images that use it, with the value the Pi would have used.

Or when its pinned tag is not one this workflow made (`unbuilt`, for instance), or is not on GHCR.

Comparing against the pinned tag rather than against the previous push is what makes it heal: a
build that failed, or a run that was cancelled, leaves the old pin in place, so the next run on
main still sees the change and builds it. Pending runs on main collapse into the newest for the
same reason, nothing is lost by skipping one.

A service whose compose entry has `build:` but no `image: ghcr.io/platanosverdes/...:${VAR}` line is
built only to check that it builds (against the previous push, or the PR's base), never pushed, and
the Pi keeps building it the old way. That is the state a new service is in until its `image:` line
is added.

Any change under a service's directory is a new tag, even a comment in the Dockerfile, and a new tag
recreates the container. For Caddy that means its live connections. Route changes in
`config/caddy/` never come here: they are a `caddy reload`, as before.

---

## Pull requests

A pull request builds every service that would be due if it merged, with the same args, on the
same runner, and stops there: it never logs in to GHCR, never pushes and never commits. A pull
request from a fork also gets a read-only token from GitHub, whatever the workflow asks for.

---

## What the Pi does

`scripts/deploy/apply.sh`, on a pass where HEAD moved:

- **Services with a published image** (`image:` on a registry and a `build:`): if the pinned tag is
  not on the Pi, `docker compose pull` it. Tags are immutable, so one already present is the one
  pinned and the registry is not asked. A source change alone does nothing here.
- If a pull fails (GHCR unreachable, the package not public), the deploy **builds that service from
  source** under the pinned tag, the old path, and logs `WARNING: could not pull ...`. A deploy
  keeps working without GHCR, at the old cost.
- **Services without one** (anything compose builds without an `image:`): built when their context
  changed, or when `versions.env` or a compose file did, exactly as before.
- Then `docker compose up -d` without `--build`, which recreates only the containers whose image
  changed. Bifrost, Caddy and the rest stay up through any push that did not move their pin.

To build from source on purpose, without waiting for CI:

```bash
# This pass only: every service with a published image is built here, from its changed context
DEPLOY_BUILD_LOCAL=1 bash scripts/deploy/apply.sh

# One service, from scratch
bash scripts/recovery/rebuild-service.sh caddy
```

Both tag the local build with the *pinned* name, so it shadows the published image of that tag
until the next pin moves it. To go back to the published one: `docker image rm` that tag and deploy.

---

## Adding a service built here

1. `services/<name>/Dockerfile`, and a `.dockerignore` that lets through only what the Dockerfile
   copies (the Go services' one is an allowlist: `*`, then `!go.mod`, `!go.sum`, `!*.go`). A
   `COPY` of anything else then fails the build in CI, visibly, rather than shipping whatever was
   lying in the directory.
2. In its compose entry, both lines:
   ```yaml
   image: ghcr.io/platanosverdes/rpi-homeserver-<name>:${<NAME>_IMAGE_TAG}
   build:
     context: ./services/<name>
     args:
       SOME_BASE_VERSION: ${SOME_BASE_VERSION}   # optional, from versions.env
   ```
3. In `versions.env`, under "built here": `<NAME>_IMAGE_TAG=unbuilt`. The first run on main sees a
   value it did not write, builds the image and pins it.

Base image versions belong in `versions.env` as build args, not in the Dockerfile: that way a bump
is a one-line commit and CI rebuilds with the same value the Pi would.

---

## Security

- Every action is pinned by full commit SHA, with the version in a comment beside it.
- `permissions: {}` at the top; `packages: write` only on the build job and `contents: write` only
  on the pin job. Only `main` logs in, pushes or commits.
- CI builds from a clean checkout of a public repo, so a build context there holds only tracked
  files: a stray `.env` or a local binary on the Pi cannot reach a published image. The
  `.dockerignore` files cover the Pi's own fallback builds, which are never published anyway.
- No build secrets: the only build args are versions.
- Provenance and SBOM attestations are off, as on the Pi, so each tag is one plain manifest.

The images are public, which is what lets the Pi pull without credentials. GHCR can create a new
package as private; if the pin job fails with `cannot be pulled anonymously`, open the package under
github.com/PlatanosVerdes?tab=packages, set it to public once, and re-run the failed job.

---

## What it does not do

- **It does not rebuild for a newer base image.** The Go Dockerfiles use `alpine:latest` and a
  floating `golang` tag, so an image is rebuilt only when something in this repo changes, and then
  takes whatever those tags point to that day. Pin them through build args to make that explicit.
- **It does not delete old tags**, on GHCR or on the Pi. A rollback is a revert of the "Deploy"
  commit: the previous tag is still on GHCR, and usually still on the Pi.
- **It builds `linux/arm64` only**, the Pi's architecture.
- **It does not build `services/deploy-webhook/`**: that is a systemd service on the host, which
  `apply.sh` reinstalls on its own when the directory changes.
- **It does not touch rpi-services** (its apps publish from their own repos) **or the `streamhalla`
  branch**, which pins its images inline and is deployed by hand.
