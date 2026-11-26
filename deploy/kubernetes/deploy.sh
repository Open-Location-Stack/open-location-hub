#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
charts="$script_dir/../charts"
: "${KUBE_CONTEXT:?set the target Kubernetes context}"
: "${HUB_NAMESPACE:?set the prepared namespace}"
: "${HUB_VALUES:?set the Hub values file}"
: "${HUB_IMAGE:?set the published Hub image}"
: "${HUB_ID:?set the persisted Hub UUID}"
: "${DB_BINDING_ID:?set the stable database binding ID}"
if [[ ! "$HUB_ID" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$ ]] || [[ "$HUB_ID" == 00000000-0000-0000-0000-000000000000 ]]; then
  echo "HUB_ID must be a nonzero lowercase UUID." >&2
  exit 2
fi

database_name="${DB_NAME:-location_hub}"
binding_name=location-hub-db-binding
pg_release=location-postgres
kafka_release=location-kafka
test_stack="${HUB_DISPOSABLE_STACK:-false}"
if [[ "$test_stack" != true && ! "$HUB_IMAGE" =~ @sha256:[a-f0-9]{64}$ ]]; then
  echo "Use a digest-qualified Hub image for this stack." >&2
  exit 2
fi

kube=(kubectl --context "$KUBE_CONTEXT" --namespace "$HUB_NAMESPACE")
helm_ns=(helm --kube-context "$KUBE_CONTEXT" --namespace "$HUB_NAMESPACE")

binding="$("${kube[@]}" get configmap "$binding_name" --ignore-not-found -o json)"
db_mode="${DB_MODE:-}"
if [[ -z "$db_mode" ]]; then
  if [[ -n "$binding" ]]; then
    db_mode="$(jq -er '.data.mode' <<<"$binding")"
  elif [[ -n "${DB_RUNTIME_SECRET:-}${DB_MIGRATION_SECRET:-}" ]]; then
    db_mode=external
  else
    db_mode=managed
  fi
fi

if [[ -n "$binding" ]] && ! jq -e --arg mode "$db_mode" --arg id "$DB_BINDING_ID" \
  --arg database "$database_name" --arg hub "$HUB_ID" \
  '.data == {mode:$mode,id:$id,database:$database,hub:$hub}' <<<"$binding" >/dev/null; then
  echo "Changing the database binding requires a database migration." >&2
  exit 1
fi

pg_options=()
kafka_options=()
db_minor_floor="${DB_MIN_VERSION_NUM:-170000}"
db_major=0
if [[ "$db_mode" == external ]]; then
  : "${DB_RUNTIME_SECRET:?set the existing database runtime Secret}"
  : "${DB_MIGRATION_SECRET:?set the existing database migration Secret}"
elif [[ "$db_mode" == managed ]]; then
  : "${PG_VALUES:?set the CloudNativePG storage and backup values file}"
  DB_RUNTIME_SECRET="${DB_RUNTIME_SECRET:-location-postgres-runtime}"
  DB_MIGRATION_SECRET="${DB_MIGRATION_SECRET:-location-postgres-migration}"
  DB_RELAY_SECRET="${DB_RELAY_SECRET:-location-postgres-relay}"
  DB_CA_SECRET="${DB_CA_SECRET:-location-postgres-ca}"
  "${kube[@]}" get crd clusters.postgresql.cnpg.io -o name >/dev/null
  "${kube[@]}" get secret "$DB_RUNTIME_SECRET" "$DB_MIGRATION_SECRET" "$DB_RELAY_SECRET" -o name >/dev/null
  current_cluster="$("${kube[@]}" get cluster "$pg_release" --ignore-not-found -o json)"
  recorded_uid="$(jq -r '.metadata.annotations["location-hub.formation-res.com/postgres-uid"] // empty' <<<"$binding")"
  actual_uid="$(jq -r '.metadata.uid // empty' <<<"$current_cluster")"
  if [[ -n "$recorded_uid" && "$recorded_uid" != "$actual_uid" ]]; then
    echo "The bound PostgreSQL cluster is missing or its UID changed. Restore or migrate it before deploying." >&2
    exit 1
  fi
  existing_image="$(jq -r '.spec.imageName // empty' <<<"$current_cluster")"
  if [[ -n "$existing_image" && -z "$binding" ]]; then
    echo "A PostgreSQL cluster already exists at the managed name. Supply its database binding explicitly." >&2
    exit 1
  fi

  if [[ -n "$existing_image" ]]; then
    pg_image="${PG_IMAGE:-$existing_image}"
    if [[ ! "$existing_image" =~ :([0-9]+)\.([0-9]+) ]]; then
      echo "The bound PostgreSQL image needs a major.minor tag." >&2
      exit 1
    fi
    db_major="${BASH_REMATCH[1]}"
  else
    if [[ -n "${POSTGRES_VERSIONS_FILE:-}" ]]; then
      release_feed="$(cat "$POSTGRES_VERSIONS_FILE")"
    else
      release_feed="$(curl -fsS --max-time 15 https://www.postgresql.org/versions.json)"
    fi
    release="$(jq -er '.[] | select(.current and .supported) | [.major,.latestMinor] | join(".")' <<<"$release_feed")"
    [[ "$release" =~ ^([0-9]+)\.([0-9]+)$ ]] || { echo "Invalid PostgreSQL release metadata." >&2; exit 1; }
    db_major="${BASH_REMATCH[1]}"
    pg_image="${PG_IMAGE:-ghcr.io/cloudnative-pg/postgresql:$release-system-trixie}"
    if [[ "$pg_image" != *":$release-"* && "$pg_image" != *":$release@"* && "$pg_image" != *":$release" ]]; then
      echo "New clusters require the current PostgreSQL release $release." >&2
      exit 1
    fi
  fi
  if [[ ! "$pg_image" =~ :([0-9]+)\.([0-9]+) ]] || [[ "${BASH_REMATCH[1]}" != "$db_major" ]]; then
    echo "PostgreSQL major upgrades require a separate maintenance operation." >&2
    exit 1
  fi
  db_minor_floor="$((db_major * 10000 + BASH_REMATCH[2]))"

  if [[ "$test_stack" != true ]]; then
    [[ "$pg_image" =~ @sha256:[a-f0-9]{64}$ ]] || {
      echo "Supply PG_IMAGE from the qualified PostgreSQL release manifest, including its digest." >&2
      exit 2
    }
    cnpg_namespace="${CNPG_OPERATOR_NAMESPACE:-cnpg-system}"
    cnpg_deployment="${CNPG_OPERATOR_DEPLOYMENT:-cnpg-controller-manager}"
    operator_image="$(kubectl --context "$KUBE_CONTEXT" --namespace "$cnpg_namespace" get deployment "$cnpg_deployment" \
      -o jsonpath='{.spec.template.spec.containers[0].image}')"
    if [[ ! "$operator_image" =~ :v?1\.([0-9]+)\.[0-9]+(@sha256:[a-f0-9]{64})?$ ]]; then
      echo "Use a stable, versioned CloudNativePG operator release." >&2
      exit 1
    fi
    operator_minor="${BASH_REMATCH[1]}"
    case "$db_major" in
      17|18) required_operator_minor=30 ;;
      19) required_operator_minor=31 ;;
      *) required_operator_minor="${CNPG_REQUIRED_MINOR:?supply the qualified operator minor for this PostgreSQL major}" ;;
    esac
    if (( operator_minor < required_operator_minor )); then
      echo "The installed CloudNativePG operator has not been qualified for PostgreSQL $db_major." >&2
      exit 1
    fi
  fi
  pg_options=(-f "$PG_VALUES" --set-string "cluster.imageName=$pg_image"
    --set-string "cluster.database=$database_name"
    --set-string "cluster.runtimeSecret=$DB_RUNTIME_SECRET"
    --set-string "cluster.migrationSecret=$DB_MIGRATION_SECRET"
    --set-string "cluster.relaySecret=$DB_RELAY_SECRET")
  if [[ "$test_stack" != true ]]; then
    pg_options+=(--set dev.singleNode=false)
  fi
else
  echo "DB_MODE must be external or managed." >&2
  exit 2
fi

hub_options=(-f "$HUB_VALUES" --set-string "image=$HUB_IMAGE"
  --set-string "identity.id=$HUB_ID" --set-string "database.name=$database_name"
  --set-string "database.mode=$db_mode" --set-string "database.runtimeSecret=$DB_RUNTIME_SECRET"
  --set-string "database.migrationSecret=$DB_MIGRATION_SECRET"
  --set-string "database.relaySecret=${DB_RELAY_SECRET:-}"
  --set-string "database.caSecret=${DB_CA_SECRET:-}"
  --set "database.minVersionNum=$db_minor_floor" --set "database.requiredMajor=$db_major")
if [[ -n "${KAFKA_VALUES:-}" ]]; then
  "${kube[@]}" get crd kafkas.kafka.strimzi.io kafkanodepools.kafka.strimzi.io \
    kafkausers.kafka.strimzi.io kafkatopics.kafka.strimzi.io -o name >/dev/null
  hub_options+=(-f "$KAFKA_VALUES"
    --set kafka.enabled=true --set kafka.insecureLocal=false
    --set-string "kafka.brokers=$kafka_release-kafka-bootstrap:9093"
    --set-string "kafka.caSecret=$kafka_release-cluster-ca-cert"
    --set-string kafka.scramMechanism=SCRAM-SHA-512)
  kafka_options=(-f "$KAFKA_VALUES")
  for role in api asset-router asset-estimator fence-worker config-relay; do
    hub_options+=(--set-string "kafka.authSecrets.$role=$kafka_release-$role"
      --set-string "kafka.usernames.$role=$kafka_release-$role")
  done
fi
if [[ "$test_stack" != true ]]; then
  hub_options+=(--set dev.insecureLocal=false --set kafka.insecureLocal=false --set network.enabled=true)
  kafka_options+=(--set dev.singleNode=false)
fi
"${kube[@]}" get secret "$DB_RUNTIME_SECRET" "$DB_MIGRATION_SECRET" -o name >/dev/null
if [[ -n "${DB_RELAY_SECRET:-}" ]]; then
  "${kube[@]}" get secret "$DB_RELAY_SECRET" -o name >/dev/null
fi
"${helm_ns[@]}" template location-hub "$charts/hub" "${hub_options[@]}" >/dev/null
if [[ "$db_mode" == managed ]]; then
  "${helm_ns[@]}" template "$pg_release" "$charts/hub-postgres" "${pg_options[@]}" >/dev/null
fi
if [[ -n "${KAFKA_VALUES:-}" ]]; then
  "${helm_ns[@]}" template "$kafka_release" "$charts/hub-kafka" "${kafka_options[@]}" >/dev/null
fi

if [[ -z "$binding" ]]; then
  "${kube[@]}" create configmap "$binding_name" --from-literal="mode=$db_mode" \
    --from-literal="id=$DB_BINDING_ID" --from-literal="database=$database_name" \
    --from-literal="hub=$HUB_ID" --dry-run=client -o json | jq '.immutable = true' | "${kube[@]}" create -f -
fi
if [[ "$db_mode" == managed ]]; then
  "${helm_ns[@]}" upgrade --install "$pg_release" "$charts/hub-postgres" "${pg_options[@]}" --wait --timeout 15m
  "${kube[@]}" wait --for=condition=Ready "cluster/$pg_release" --timeout=15m
  actual_uid="$("${kube[@]}" get cluster "$pg_release" -o jsonpath='{.metadata.uid}')"
  "${kube[@]}" annotate "configmap/$binding_name" "location-hub.formation-res.com/postgres-uid=$actual_uid" --overwrite
fi
if [[ -n "${DB_CA_SECRET:-}" ]]; then
  "${kube[@]}" get secret "$DB_CA_SECRET" -o name >/dev/null
fi
if [[ -n "${KAFKA_VALUES:-}" ]]; then
  "${helm_ns[@]}" upgrade --install "$kafka_release" "$charts/hub-kafka" "${kafka_options[@]}" --wait --timeout 15m
  "${kube[@]}" wait --for=condition=Ready "kafka/$kafka_release" --timeout=15m
  "${kube[@]}" wait --for=condition=Ready kafkatopic,kafkauser -l "strimzi.io/cluster=$kafka_release" --timeout=5m
fi
"${helm_ns[@]}" upgrade --install location-hub "$charts/hub" "${hub_options[@]}" --wait --wait-for-jobs --timeout 15m
"${helm_ns[@]}" test location-hub --timeout 5m
