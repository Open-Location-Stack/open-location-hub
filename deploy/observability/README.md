# Open Location Hub monitoring

Start the [local demo](../../local-hub/README.md) with `just local-hub-up`.
To add the same monitoring services to the Kafka stack, use
`just compose-shadow-monitoring-up` after applying the database migrations.
Start the router, estimator and outbox relay through the monitoring-enabled
stack to collect their metrics as well.

```bash
docker compose --env-file .env.example -f docker-compose.yml \
  -f deploy/kafka/compose.shadow.yaml -f deploy/observability/compose.hub.yaml \
  run --rm --no-deps app asset-estimator
```

Use the same command with `asset-router` and `config-relay` in separate
processes. The [configuration guide](../../docs/configuration.md) specifies
the required Kafka topics, credentials and options for each role. Supply the
persisted Hub ID as `HUB_ID` to Kafka-only workers so the Hub filter can match
their metrics to the API and relay. Those workers can run without database
credentials.

Perses provisions the Hub project, datasource and five dashboards from
[`resources/`](resources) at startup. Open the
[Open Location Hub project](http://localhost:8090/projects/location-hub).
For the airport replay, start with **Pipeline overview**. Compare arrivals,
completed work, queue waits and drops. Use **Worker performance** to isolate
the stage, operation or instance and compare queue, handler and commit
latency. The REST section groups requests by route template and method, and
the RPC section groups them by operation. **Rule state machines** shows
evaluation rate, count, p50/p95/p99 and transitions separately for fence
membership, collision pairs and proximity hysteresis. **Configuration and
recovery** and **Realtime delivery** cover outbox, restore and subscriber
behavior. Panels for observations, asset inputs, estimates and configuration
revisions use different units. Rate and range counts use the exported
cumulative counters. Use committed Kafka records for exact reconciliation.

Prometheus preserves process identity on cumulative OTLP metrics through
`job` and `instance`. It also promotes `hub.id` and
`deployment.environment.name` for dashboard filters. The seven-day local
Prometheus retention covers metrics history. Kafka event retention is
configured separately. The local Alertmanager receives alerts for sustained
drops and missing Hub telemetry. It uses a local receiver with no external
delivery destinations.

Run the configuration checks against the demo project.

```bash
percli lint -d deploy/observability/resources
docker compose -f local-hub/demo-compose.yml --env-file local-hub/demo.env \
  exec prometheus promtool check config /etc/prometheus/prometheus.yaml
docker compose -f local-hub/demo-compose.yml --env-file local-hub/demo.env \
  exec alertmanager amtool check-config /etc/alertmanager/alertmanager.yaml
curl -fsS http://localhost:8090/api/v1/projects/location-hub/dashboards
```

Run `promtool test rules deploy/observability/rules.test.yaml` to verify alert
behavior without waiting for a live fault.

When using a shared monitoring stack, preserve the documented metric
translation and resource labels. Scope Prometheus access through a read-only
query proxy or backend authorization and put the Perses UI behind
authentication. The demo publishes Perses on the loopback interface.
