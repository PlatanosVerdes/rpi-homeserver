# sleipnir: the VPN tunnel panel

`https://sleipnir.<domain>` lists every gluetun tunnel on this machine, found by the
`sleipnir.enable` label, and moves its exit to another country or server in place: whatever uses the
tunnel carries on after a pause of a few seconds. It does not decide which container uses which
tunnel; that stays in compose. sleipnir is its own app, pinned here as `SLEIPNIR_VERSION`; how a
switch works, its labels and its settings are in its docs:
[switching](https://github.com/PlatanosVerdes/sleipnir/blob/main/docs/switching.md), [tunnels](https://github.com/PlatanosVerdes/sleipnir/blob/main/docs/tunnels.md) and [configuration](https://github.com/PlatanosVerdes/sleipnir/blob/main/docs/configuration.md).

What is specific to this repo:

## The control-server key

sleipnir drives each gluetun through its control server, and every route needs a key:

```bash
docker run --rm qmcgaw/gluetun:v3.41.3 genkey
```

Put the output in `.env` as `GLUETUN_API_KEY`. Every gluetun here sets
`HTTP_CONTROL_SERVER_AUTH_DEFAULT_ROLE` from it. gluetun refuses to start with an empty key, so
compose refuses to run without one (`${GLUETUN_API_KEY:?}`): a missing key fails the deploy instead of
taking a running tunnel down.

## What a switch does not do

A switch lives in gluetun's memory. If a gluetun container is recreated (a deploy that touches it, a
reboot), it goes back to `SERVER_COUNTRIES` from compose, which reads `VPN_COUNTRY` /
`VPN_TEST_COUNTRY` in `.env`. To make a country permanent, change `.env`.

## The test tunnel and huginn

`gluetun-test` is a second tunnel to try the panel on, exiting through `VPN_TEST_COUNTRY` (Turkey by
default). It must differ from the main tunnel's country: both use the same WireGuard key, and two
sessions on one server drop each other. The panel warns when a switch would put two tunnels in the
same country.

`huginn` (same image as sleipnir, `command: ["huginn"]`) lives in `gluetun-test`'s network namespace
and reports the exit it sees at request time. Its JSON at `http://gluetun-test:8080/json` is the test
tunnel's probe, so a switch there only counts as done when huginn agrees.

Before the test tunnel's first start, copy `appdata/gluetun/servers.json` into
`appdata/gluetun-test/`: otherwise gluetun picks from the list baked into its image, which still names
servers NordVPN has retired.

## Other machines

Each machine runs its own panel and only talks to its own gluetuns. To show other machines on one
page, set on the machine whose page you open:

```bash
SLEIPNIR_NAME=Raspberry
SLEIPNIR_PEERS=streamhalla=http://100.120.107.32:8092
SLEIPNIR_TOKEN=<this machine's own random string>
SLEIPNIR_PEER_TOKENS=streamhalla=<streamhalla's SLEIPNIR_TOKEN>
```

One token per machine, so the one that leaks from a peer does not open this one. A peer missing
from `SLEIPNIR_PEER_TOKENS` is sent this machine's own token.

Peers answer on `:8092`, published on the Tailscale address only, and that listener exists only when
`SLEIPNIR_TOKEN` is set. A peer that does not answer shows as "No answer".
