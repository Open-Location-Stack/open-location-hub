# Deploy Open Location Hub on Kubernetes

Use [deploy.sh](deploy.sh) with an explicit Kubernetes context, a prepared
namespace and one Hub per namespace. The Hub chart installs the API,
asset router, motion estimator and configuration relay. It runs the
embedded Goose migrations before the application rollout and runs a
service health check afterward. PostgreSQL and Kafka have independent
lifecycles and separate Helm releases. The API and configuration relay
each use a single replica while they own compatibility state and
publication. Router and estimator replicas can be configured independently
and share fixed Kafka partition counts.

## Prepare the stack

Set the deployment identity and the app configuration.

```bash
export KUBE_CONTEXT=my-cluster
export HUB_NAMESPACE=location-hub
export HUB_VALUES=deploy/kubernetes/examples/hub.yaml
export HUB_IMAGE='<registry>/<repository>:<release>@sha256:<digest>'
export HUB_ID='<persisted-lowercase-hub-uuid>'
export DB_BINDING_ID='<stable-stack-database-id>'
```

Copy the example values and set the identity provider, MQTT broker,
network peers and optional HTTPS ingress to the stack's endpoints.
The namespace needs its own OIDC or static key configuration and a
Secret with `permissions.yaml`. Supply the MQTT URL in the configured
Secret under `url`. Define permitted egress to PostgreSQL, Kafka, MQTT,
your identity provider and your Collector as needed. Give the ingress
controller access to the API port. For a shared Collector, set
`telemetry.metricsEndpoint` to its OTLP metrics URL and
`telemetry.deployment` to the stack name.

The script reads and stores an immutable database binding in the target
namespace. If `DB_MODE` is omitted it uses the recorded mode, or selects
external when existing database Secret names are supplied. Otherwise it
selects managed. Any failed check stops the release. The database also
stores a binding with its Hub UUID, and the API and relay verify it
before startup. Keep the ConfigMap with the installation's records.
A rebind or move needs a database migration. The script records the
managed Cluster UID after readiness so a missing or replaced Cluster
does not cause an empty replacement database to be installed.

## Reuse an existing PostgreSQL database

Select a dedicated writable database on PostgreSQL 17 or newer with a
current patch from the database owner. Connect directly to the writable
primary for database preparation so the migration can hold a session
lock. Set `DB_NAME` if the database is
named differently from `location_hub`. Provision three separate login
roles. The migration role owns the database and the Hub's existing
tables when adopting an earlier installation. The runtime and relay
roles need connection and schema usage with no CREATE privilege. The
migration job grants the table privileges required by each role.

Provision the namespace-local connection Secrets, each with a
`POSTGRES_URL` containing its role credentials, database name and
`sslmode=verify-full`. With a private CA, set `DB_CA_SECRET` and include
`sslrootcert=/etc/location-hub/postgres/ca.crt` in each URL. Supply the CA at
key `ca.crt`. Set `DB_MIN_VERSION_NUM` to enforce the minimum patch agreed
with the database owner.

```bash
export DB_RUNTIME_SECRET=existing-hub-runtime
export DB_MIGRATION_SECRET=existing-hub-migration
export DB_RELAY_SECRET=existing-hub-relay
deploy/kubernetes/examples/reuse-postgresql.sh
```

For existing Kafka, use the brokers, ACLs and four credential Secrets in
the Hub values. The Secret keys are `username` and `password` unless the
username is supplied via the role's `kafka.usernames` entry. Provision the
topics beforehand and apply an independent retention override per event
topic when needed. The defaults match the managed Kafka chart. Omit
`KAFKA_VALUES` to use existing Kafka. To provision Kafka alongside an
existing PostgreSQL database, set `KAFKA_VALUES` before running the same
script.

## Provision PostgreSQL and Kafka

Install the CloudNativePG and Strimzi operators and their CRDs first.
The example uses a three-instance PostgreSQL cluster across zones
with a synchronous standby and a Barman Cloud ObjectStore for WAL and
backups. Prepare the StorageClass, ObjectStore and the Barman Cloud
plugin separately. Populate three namespace-local `kubernetes.io/basic-auth`
Secrets with `username` and `password` for the chart's named PostgreSQL
roles. The default Secret names are `location-postgres-migration`,
`location-postgres-runtime` and `location-postgres-relay`. The script looks
up and mounts the CloudNativePG CA Secret after provisioning.

```bash
export PG_VALUES=deploy/kubernetes/examples/postgres.yaml
export KAFKA_VALUES=deploy/kubernetes/examples/kafka.yaml
export PG_IMAGE='<current-cloudnative-pg-postgresql-image-with-digest>'
deploy/kubernetes/examples/create-postgresql.sh
```

For a new cluster, the installer checks the image tag against PostgreSQL's
current stable version feed. `POSTGRES_VERSIONS_FILE` can supply a copy of
that feed from release CI. Use an approved image with the tag and digest in
production. Reruns retain the existing PostgreSQL major. A change of major
requires its own database maintenance operation. As of 26 November 2026,
PostgreSQL **19.0** is current and PostgreSQL 19 support is available in the
CloudNativePG **1.31** release candidate. The installer requires a compatible
stable operator for production. [PostgreSQL version feed](https://www.postgresql.org/versions.json)
[CloudNativePG release notes](https://cloudnative-pg.io/releases/cloudnative-pg-1-31.0-rc1-released/)

The managed Kafka example uses Strimzi, a private TLS listener and
SCRAM-SHA-512 with a distinct principal for each role. Kafka event topics
default to `kafka.logRetentionMs=21600000` (six hours). Override individual
logs in `kafka.topicRetentionMs` using the keys in `kafka.topics`. The
estimator checkpoint topic stores current state with a compaction policy.
Broker resources and topic declarations remain outside the application release.

Set `kafka.estimatorConfigSecret` to enable revision-aware selection. Its
`POSTGRES_URL` key should use a dedicated read-only role with the table access
described in the [configuration guide](../../docs/configuration.md#asset-motion-estimator).
The estimator uses `kafka.configMaxStale` as its configuration freshness grace.
Choose fresh estimate and checkpoint topic names on the first rollout of this
mode, and provision the same names in the managed Kafka values.

Enable `kafka.fenceRouting` to produce shadow fence evaluation inputs.
Keep the configuration secret enabled and extend its reader grants to
`zones` and `fences`. Set `kafka.topics.fenceEvaluations` to the same value
in both charts. The broker chart creates that topic and grants the estimator
write access. Use a fresh estimate and checkpoint topic for the first
routing rollout. Set `kafka.topicRetentionMs.fenceEvaluations` to override
the shared six-hour event-log retention.

## Disposable clusters

`HUB_DISPOSABLE_STACK=true` allows initial testing with a release candidate,
a tagged image, the chart's single-node stateful values and explicitly
enabled local-only networking options. Keep these settings confined to a
separate Kubernetes context and namespace. The migration and Helm smoke
test check database binding and API health. Run the
[airport functional profile](../../connectors/replay/benchmarks/opensky-germany-2026-04-08/README.md#functional-profile)
for end-to-end verification.
