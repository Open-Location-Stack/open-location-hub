# Software Documentation

Reference documentation for local setup, hub architecture, configuration,
authentication, RPC behavior, and connector development.

Start here if you want the local runtime:

- [`docs/getting-started.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/getting-started.md)

If you want the published container instead of a local build, the current Docker
Hub repository is
[`tryformation/openlocationhub`](https://hub.docker.com/r/tryformation/openlocationhub).
As of 2026-04-27, the published release tag is `0.1.0`, and `latest` points to
that same image.

Core hub docs:

- [`docs/architecture.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/architecture.md)
- [`docs/configuration.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/configuration.md)
- [`docs/auth.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/auth.md)
- [`docs/rpc.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/rpc.md)

Connector docs:

- [`docs/connectors.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/connectors.md)
- [`docs/connectors-websocket.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/connectors-websocket.md)
- [`docs/connectors-mqtt.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/docs/connectors-mqtt.md)

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
