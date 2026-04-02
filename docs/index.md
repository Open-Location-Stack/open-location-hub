# Software Documentation

Reference documentation for hub architecture, configuration, authentication,
RPC behavior, and connector development.

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
[`connectors/local-hub/README.md`](../connectors/local-hub/README.md).
Connector examples currently include
[`connectors/gtfs/README.md`](../connectors/gtfs/README.md)
and
[`connectors/opensky/README.md`](../connectors/opensky/README.md),
plus
[`connectors/replay/README.md`](../connectors/replay/README.md).
