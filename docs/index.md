# Software Documentation

Reference documentation for local setup, hub architecture, configuration,
authentication, RPC behavior, and connector development.

Start here if you want the local runtime:

- [`docs/getting-started.md`](getting-started.md)

If you want the published container instead of a local build, the current Docker
Hub repository is
[`tryformation/openlocationhub`](https://hub.docker.com/r/tryformation/openlocationhub).
As of 2026-06-11, the published release tag is `0.1.8`, and `latest` points to
the current release image.

The companion command line client is
[`Open-Location-Stack/open-location-hub-cli`](https://github.com/Open-Location-Stack/open-location-hub-cli).
It installs as `olh` and covers local login, resource CRUD, ingest helpers,
WebSocket streams, and RPC calls.

Homebrew install:

```bash
brew tap jillesvangurp/tap
brew install jillesvangurp/tap/open-location-hub-cli
```

Core hub docs:

- [`docs/architecture.md`](architecture.md)
- [`docs/configuration.md`](configuration.md)
- [`docs/auth.md`](auth.md)
- [`docs/rpc.md`](rpc.md)
- [`deploy/hetzner/README.md`](../deploy/hetzner/README.md)

Connector docs:

- [`docs/connectors.md`](connectors.md)
- [`docs/connectors-websocket.md`](connectors-websocket.md)
- [`docs/connectors-mqtt.md`](connectors-mqtt.md)

Connector demonstrators live outside the hub runtime under
[`connectors/`](../connectors).
Shared connector-agnostic utility scripts live under
[`scripts/`](../scripts).
The shared local runtime is documented in
[`local-hub/README.md`](../local-hub/README.md).
Connector examples currently include
[`connectors/gtfs/README.md`](../connectors/gtfs/README.md)
and
[`connectors/opensky/README.md`](../connectors/opensky/README.md),
plus
[`connectors/replay/README.md`](../connectors/replay/README.md).
