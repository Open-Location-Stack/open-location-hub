# Software Documentation

Reference documentation for local setup, hub architecture, configuration,
authentication, RPC behavior, and connector development.

Start here if you want the local runtime:

- [`docs/getting-started.md`](docs/getting-started.md)

If you want the published container instead of a local build, the current Docker
Hub repository is
[`tryformation/openlocationhub`](https://hub.docker.com/r/tryformation/openlocationhub).
As of 2026-06-10, the published release tag is `0.1.5`, and `latest` points to
that same image.

Core hub docs:

- [`docs/architecture.md`](docs/architecture.md)
- [`docs/configuration.md`](docs/configuration.md)
- [`docs/auth.md`](docs/auth.md)
- [`docs/rpc.md`](docs/rpc.md)
- [`deploy/hetzner/README.md`](deploy/hetzner/README.md)

Connector docs:

- [`docs/connectors.md`](docs/connectors.md)
- [`docs/connectors-websocket.md`](docs/connectors-websocket.md)
- [`docs/connectors-mqtt.md`](docs/connectors-mqtt.md)

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
