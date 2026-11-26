# Local Hub starter stack

Run `just local-hub-up` from the repository root. The first run creates
`local-hub/demo.env` from the example. The stack starts Open Location Hub,
PostgreSQL, Mosquitto, Dex, the OpenTelemetry Collector, Prometheus,
Alertmanager and Perses. A migration container prepares PostgreSQL before the
Hub starts. The stack is for local development, demos and connector replay.

- [Hub API](http://localhost:8080)
- WebSocket at `ws://localhost:8080/v2/ws/socket`
- [Perses dashboards](http://localhost:8090/projects/location-hub)
- `just local-hub-token` fetches an administrator token from Dex.
- `just local-hub-logs` follows the Hub logs.
- `just local-hub-down` stops the stack and keeps its data.

Dex includes `admin@example.com`, `reader@example.com` and `owner@example.com`
with the fixture password `testpass123`.

## Metrics and state

The [monitoring guide](../deploy/observability/README.md) describes the dashboards
and the Kafka shadow profile. Perses binds to loopback on `DEMO_PERSES_PORT`.
Prometheus, Alertmanager and the Collector use private Compose networks.
PostgreSQL persists under `local-hub/state/postgres` by default. Prometheus
and Alertmanager use Compose volumes. Perses loads dashboard definitions from
Git at startup.

Existing SigNoz installations can keep their historical metrics. Stop the old
SigNoz Compose project independently after saving any history you need. If it
still uses port 8090, set `DEMO_PERSES_PORT` to another free port before starting
this stack.

The demo uses the existing PostgreSQL 17 data layout. Upgrading an existing
data directory to a new major version requires a PostgreSQL migration.

See the [getting started guide](../docs/getting-started.md) for client
examples and the [connector guide](../docs/connectors.md) for replay sources.
