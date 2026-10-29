{{/*
Expand the name of the chart.
*/}}
{{- define "shopping-cart.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "shopping-cart.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Chart name and version as used by the chart label.
*/}}
{{- define "shopping-cart.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "shopping-cart.labels" -}}
helm.sh/chart: {{ include "shopping-cart.chart" . }}
{{ include "shopping-cart.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels. Every Deployment and Service also pins
app.kubernetes.io/component (api): a Service selecting on these two alone would
select every component a future change adds.
*/}}
{{- define "shopping-cart.selectorLabels" -}}
app.kubernetes.io/name: {{ include "shopping-cart.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "shopping-cart.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "shopping-cart.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Name of the Secret holding DATABASE_URL: the operator's own
(database.existingSecret) or the one this chart creates from database.url.
*/}}
{{- define "shopping-cart.databaseSecretName" -}}
{{- if .Values.database.existingSecret }}
{{- .Values.database.existingSecret }}
{{- else }}
{{- include "shopping-cart.fullname" . }}-database
{{- end }}
{{- end }}

{{/*
Image reference. The release job publishes the image as v<version> (and packages
the chart with appVersion <version>), so an empty image.tag means "v" + appVersion.
*/}}
{{- define "shopping-cart.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default (printf "v%s" .Chart.AppVersion) }}
{{- end }}

{{/*
MANDATORY: cmd/api exits at boot without DATABASE_URL (real Postgres only, no
in-memory mode). Fails the render naming the values instead of crash-looping.
*/}}
{{- define "shopping-cart.requireDatabase" -}}
{{- if not (or .Values.database.url .Values.database.existingSecret) -}}
{{- fail "shopping-cart requires database.url or database.existingSecret to be set (DATABASE_URL): the service runs on real Postgres only and exits at boot without it." -}}
{{- end -}}
{{- end -}}

{{/*
MANDATORY: cmd/api exits at boot without KAFKA_BROKERS (outbox relay, the three
consumers, /readyz). No default broker and no way to switch Kafka off.
*/}}
{{- define "shopping-cart.requireKafka" -}}
{{- $_ := required "shopping-cart requires kafka.brokers to be set (KAFKA_BROKERS, comma-separated): the service runs on real Kafka only and exits at boot without it." .Values.kafka.brokers -}}
{{- end -}}

{{/*
MANDATORY: cmd/api exits at boot without DELIVERY_PROMISE_URL (the cart quotes
delivery offers through delivery-promise).
*/}}
{{- define "shopping-cart.requireDeliveryPromise" -}}
{{- $_ := required "shopping-cart requires deliveryPromise.url to be set (DELIVERY_PROMISE_URL): the cart calls delivery-promise to quote delivery offers and cmd/api exits at boot without it." .Values.deliveryPromise.url -}}
{{- end -}}

{{/*
The three consumers always run under STABLE group ids that must come from values
(cmd/api exits at boot without any of them). A stable id is what lets a
rescheduled pod resume from its committed offsets; it is never derived from the
pod name.
*/}}
{{- define "shopping-cart.requireConsumerGroups" -}}
{{- range $c := list (dict "key" "offerAvailabilityConsumerGroup" "env" "OFFER_AVAILABILITY_CONSUMER_GROUP") (dict "key" "pricingConsumerGroup" "env" "PRICING_CONSUMER_GROUP") (dict "key" "checkoutConsumerGroup" "env" "CHECKOUT_CONSUMER_GROUP") -}}
{{- if not (index $.Values.config $c.key) -}}
{{- fail (printf "config.%s is required (%s): the consumer always runs on real Kafka and cmd/api exits at boot without a group. Use a STABLE, service-prefixed id, never the pod name." $c.key $c.env) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
extraEnv may not bring back what the chart owns: no *_MODE variable (this
service has no mode: Postgres and Kafka are the only runtime), no
*_CONSUMER_GROUP (the group ids are config.* values), and none of the variables
the chart renders itself (the same variable is never set twice).
*/}}
{{- define "shopping-cart.requireCleanExtraEnv" -}}
{{- $owned := list "HTTP_ADDR" "LOG_LEVEL" "DATABASE_URL" "MIGRATIONS_DATABASE_URL" "KAFKA_BROKERS" "DELIVERY_PROMISE_URL" "DELIVERY_PROMISE_TIMEOUT" "CART_CURRENCY" "ABANDON_SWEEP_INTERVAL" "PURGE_SWEEP_INTERVAL" "OUTBOX_RELAY_INTERVAL" "OUTBOX_MAX_ATTEMPTS" "SHUTDOWN_DRAIN_DELAY" "OFFER_AVAILABILITY_CONSUMER_GROUP" "PRICING_CONSUMER_GROUP" "CHECKOUT_CONSUMER_GROUP" -}}
{{- range .Values.extraEnv -}}
{{- $name := toString .name -}}
{{- if hasSuffix "_MODE" $name -}}
{{- fail (printf "extraEnv sets %s: *_MODE variables do not exist here - shopping-cart runs on real Postgres and real Kafka only, with no permissive, off or in-memory mode." $name) -}}
{{- end -}}
{{- if hasSuffix "_CONSUMER_GROUP" $name -}}
{{- fail (printf "extraEnv sets %s: the consumer group ids are the stable values config.offerAvailabilityConsumerGroup, config.pricingConsumerGroup and config.checkoutConsumerGroup." $name) -}}
{{- end -}}
{{- if has $name $owned -}}
{{- fail (printf "extraEnv sets %s, which the chart renders from its own values: use that value instead." $name) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
SHUTDOWN_DRAIN_DELAY in whole seconds (the binary accepts Go durations; the chart
accepts "Ns", "Nm" and "0" because it needs the number to size the grace
period). Unset = the binary's default, 5s.
*/}}
{{- define "shopping-cart.drainSeconds" -}}
{{- $raw := toString .Values.config.shutdownDrainDelay -}}
{{- if eq $raw "" -}}5
{{- else if eq $raw "0" -}}0
{{- else if regexMatch "^[0-9]+s$" $raw -}}{{ trimSuffix "s" $raw }}
{{- else if regexMatch "^[0-9]+m$" $raw -}}{{ mul 60 (int (trimSuffix "m" $raw)) }}
{{- else -}}
{{- fail (printf "config.shutdownDrainDelay is %q - want whole seconds (\"5s\"), minutes (\"1m\") or \"0\"." $raw) -}}
{{- end -}}
{{- end -}}

{{/*
The graceful shutdown is sequential (cmd/api/main.go): SHUTDOWN_DRAIN_DELAY,
the HTTP drain (10s), then the five workers - the offer-availability, pricing
and checkout consumers and the AbandonInactive / PurgeAbandonedCarts sweeps -
awaited up to 10s each (50s), and the outbox relay (10s). A grace period that
does not exceed drain + 70s can SIGKILL a consumer, a sweep or the relay
mid-drain, stranding an uncommitted offset or an in-flight outbox batch.
*/}}
{{- define "shopping-cart.requireGracePeriod" -}}
{{- $need := add (int (include "shopping-cart.drainSeconds" .)) 70 -}}
{{- if le (int .Values.terminationGracePeriodSeconds) $need -}}
{{- fail (printf "terminationGracePeriodSeconds is %v - it must exceed %v (SHUTDOWN_DRAIN_DELAY %vs + HTTP 10s + 5 workers 10s each + relay 10s): a shorter period SIGKILLs a worker mid-drain." .Values.terminationGracePeriodSeconds $need (include "shopping-cart.drainSeconds" .)) -}}
{{- end -}}
{{- end -}}

{{/*
The port of HTTP_ADDR is the container port the probes and the Service target.
*/}}
{{- define "shopping-cart.requireHttpPort" -}}
{{- $port := trimPrefix ":" (regexFind ":[0-9]+$" (toString .Values.config.httpAddr)) -}}
{{- if ne $port (toString .Values.service.targetPort) -}}
{{- fail (printf "config.httpAddr is %q but service.targetPort is %v: the container port, the probes and the Service must agree." .Values.config.httpAddr .Values.service.targetPort) -}}
{{- end -}}
{{- end -}}

{{/*
Every render-time requirement of the real runtime, in one include. The api
Deployment (always rendered) includes it, so a mis-set release fails
`helm template` / `helm install`.
*/}}
{{- define "shopping-cart.requireRealRuntime" -}}
{{- include "shopping-cart.requireDatabase" . -}}
{{- include "shopping-cart.requireKafka" . -}}
{{- include "shopping-cart.requireDeliveryPromise" . -}}
{{- include "shopping-cart.requireConsumerGroups" . -}}
{{- include "shopping-cart.requireCleanExtraEnv" . -}}
{{- include "shopping-cart.requireGracePeriod" . -}}
{{- include "shopping-cart.requireHttpPort" . -}}
{{- end -}}
