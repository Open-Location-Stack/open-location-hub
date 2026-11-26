{{- define "hub.commonLabels" -}}
app.kubernetes.io/name: open-location-hub
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "hub.databaseBindingEnv" -}}
- name: HUB_ID
  value: {{ required "Set identity.id to the persisted Hub UUID" .Values.identity.id | quote }}
- name: HUB_LABEL
  value: {{ .Values.identity.label | quote }}
- name: HUB_DB_NAME
  value: {{ .Values.database.name | quote }}
- name: HUB_DB_MODE
  valueFrom:
    configMapKeyRef:
      name: {{ .Values.database.bindingConfigMap }}
      key: mode
- name: HUB_DB_BINDING_ID
  valueFrom:
    configMapKeyRef:
      name: {{ .Values.database.bindingConfigMap }}
      key: id
- name: HUB_DB_MIN_VERSION_NUM
  value: {{ .Values.database.minVersionNum | quote }}
- name: HUB_DB_REQUIRED_MAJOR
  value: {{ .Values.database.requiredMajor | quote }}
- name: HUB_DB_INSECURE_LOCAL
  value: {{ .Values.dev.insecureLocal | quote }}
{{- end -}}

{{- define "hub.databaseConnEnv" -}}
{{- $db := .root.Values.database -}}
{{- $secret := required (printf "Set database.%sSecret" .role) (index $db (printf "%sSecret" .role)) -}}
- name: {{ .urlVar }}
  {{- if eq $db.mode "managed" }}
  {{- $ssl := "sslmode=verify-full sslrootcert=/etc/location-hub/postgres/ca.crt" }}
  {{- if .root.Values.dev.insecureLocal }}
  {{- $ssl = "sslmode=disable" }}
  {{- end }}
  value: {{ printf "host=%s port=5432 dbname=%s user=%s %s" $db.host $db.name (index $db (printf "%sUser" .role)) $ssl | quote }}
- name: {{ .passwordVar }}
  valueFrom:
    secretKeyRef:
      name: {{ $secret }}
      key: password
  {{- else }}
  valueFrom:
    secretKeyRef:
      name: {{ $secret }}
      key: POSTGRES_URL
  {{- end }}
{{- end -}}

{{- define "hub.kafkaEnv" -}}
{{- $root := .root -}}
{{- $kafka := $root.Values.kafka -}}
{{- $secret := default $kafka.authSecret (index $kafka.authSecrets .role) -}}
- name: KAFKA_SHADOW_BROKERS
  value: {{ required "Set kafka.brokers or supply the managed Kafka service" $kafka.brokers | quote }}
- name: KAFKA_SHADOW_INSECURE_LOCAL
  value: {{ $kafka.insecureLocal | quote }}
{{- if $kafka.caSecret }}
- name: KAFKA_SHADOW_CA_FILE
  value: /etc/location-hub/kafka/ca.crt
{{- end }}
{{- if $secret }}
- name: KAFKA_SHADOW_USERNAME
  {{- with index $kafka.usernames .role }}
  value: {{ . | quote }}
  {{- else }}
  valueFrom:
    secretKeyRef:
      name: {{ $secret }}
      key: username
  {{- end }}
- name: KAFKA_SHADOW_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ $secret }}
      key: password
- name: KAFKA_SHADOW_SCRAM_MECHANISM
  value: {{ $kafka.scramMechanism | quote }}
{{- else if not (and $root.Values.dev.insecureLocal $kafka.insecureLocal) }}
{{- fail (printf "Set Kafka credentials for %s" .role) }}
{{- end }}
- name: KAFKA_SHADOW_TOPIC
  value: {{ $kafka.topics.observations | quote }}
- name: KAFKA_ASSET_INPUT_TOPIC
  value: {{ $kafka.topics.assetInputs | quote }}
- name: KAFKA_ESTIMATE_TOPIC
  value: {{ $kafka.topics.estimates | quote }}
- name: KAFKA_ESTIMATOR_STATE_TOPIC
  value: {{ $kafka.topics.estimatorState | quote }}
- name: KAFKA_CONFIG_TOPIC
  value: {{ $kafka.topics.configuration | quote }}
- name: KAFKA_ASSET_ROUTER_GROUP
  value: {{ $kafka.routerGroup | quote }}
- name: HUB_CONFIG_HISTORY
  value: {{ $kafka.configHistory | quote }}
{{- end -}}

{{- define "hub.containerSecurity" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
capabilities:
  drop: ["ALL"]
{{- end -}}

{{- define "hub.podSecurity" -}}
runAsNonRoot: true
runAsUser: 10001
runAsGroup: 10001
seccompProfile:
  type: RuntimeDefault
{{- end -}}
