#!/usr/bin/env bash
# Chart render assertions for shopping-cart: Postgres and Kafka are the only
# runtime, so the mandatory configuration has no silent default, no *_MODE is
# ever rendered, the three consumer group ids are stable chart values, the pod
# is hardened and has room to shut down, and the env cmd/api reads is the env
# the chart renders.
# Run: bash charts/shopping-cart/tests/render_test.sh   (needs helm)
set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_dir="$(cd "$chart_dir/../.." && pwd)"
release=shopping-cart
# Dummy values, no password: the chart refuses to render without a database
# source, Kafka brokers, the delivery-promise URL and the three consumer
# groups. None is ever dialled.
groups=(offerAvailabilityConsumerGroup:shopping-cart-offer-availability
  pricingConsumerGroup:shopping-cart-pricing
  checkoutConsumerGroup:shopping-cart-checkout)
base=(--set database.url=postgres://u@example.invalid:5432/db
  --set kafka.brokers=kafka.example.invalid:9092
  --set deliveryPromise.url=http://delivery-promise.example.invalid:8080)
for g in "${groups[@]}"; do base+=(--set "config.${g%%:*}=${g#*:}"); done
# Every optional value set, so every env var cmd/api can read is rendered.
everything=(--set config.cartCurrency=USD --set config.abandonSweepInterval=30m
  --set config.purgeSweepInterval=3h --set config.outboxRelayInterval=2s
  --set config.shutdownDrainDelay=5s --set deliveryPromise.timeout=3s
  --set database.migrationsUrl=postgres://u@example.invalid:5432/direct
  --set autoscaling.api.enabled=true --set ingress.enabled=true --set gatewayApi.enabled=true)

fail() { echo "FAIL: $*" >&2; exit 1; }
command -v helm >/dev/null || fail "helm is not installed"

configmap="$(helm template "$release" "$chart_dir" "${base[@]}" --show-only templates/configmap.yaml)"
api_deployment="$(helm template "$release" "$chart_dir" "${base[@]}" --show-only templates/deployment.yaml)"
configmap_name="$(awk '/^  name:/ {print $2; exit}' <<<"$configmap")"
[[ -n "$configmap_name" ]] || fail "could not read the ConfigMap name"

# Every non-secret config value is rendered into the chart ConfigMap, and the api
# Deployment -- the only workload -- must mount it with envFrom, or a value that
# is only in the ConfigMap silently does nothing.
grep -A3 'envFrom:' <<<"$api_deployment" | grep -q 'configMapRef:' \
  || fail "api Deployment does not mount the ConfigMap via envFrom"
grep -A3 'envFrom:' <<<"$api_deployment" | grep -q "name: $configmap_name" \
  || fail "api Deployment envFrom does not reference ConfigMap $configmap_name"
grep -q 'checksum/config:' <<<"$api_deployment" || fail "the pods must roll when the ConfigMap changes (checksum/config)"

# A tuned config.* value reaches the ConfigMap the api mounts and the container env.
tuned="$(helm template "$release" "$chart_dir" "${base[@]}" --set config.outboxRelayInterval=7s --set config.outboxMaxAttempts=5 \
  --set config.cartCurrency=EUR --set config.abandonSweepInterval=15m --set config.purgeSweepInterval=2h \
  --set deliveryPromise.timeout=3s --show-only templates/configmap.yaml)"
grep -q 'OUTBOX_RELAY_INTERVAL: "7s"' <<<"$tuned" || fail "config.outboxRelayInterval is not rendered into the ConfigMap"
grep -q 'OUTBOX_MAX_ATTEMPTS: "5"' <<<"$tuned" || fail "config.outboxMaxAttempts is not rendered into the ConfigMap"
grep -q 'CART_CURRENCY: "EUR"' <<<"$tuned" || fail "config.cartCurrency is not rendered into the ConfigMap"
grep -q 'ABANDON_SWEEP_INTERVAL: "15m"' <<<"$tuned" || fail "config.abandonSweepInterval is not rendered into the ConfigMap"
grep -q 'PURGE_SWEEP_INTERVAL: "2h"' <<<"$tuned" || fail "config.purgeSweepInterval is not rendered into the ConfigMap"
grep -q 'DELIVERY_PROMISE_TIMEOUT: "3s"' <<<"$tuned" || fail "deliveryPromise.timeout is not rendered into the ConfigMap"

# The relay attempt cap defaults to 20 and 0 (unlimited) is rendered as is.
grep -q 'OUTBOX_MAX_ATTEMPTS: "20"' <<<"$configmap" || fail "OUTBOX_MAX_ATTEMPTS must default to 20"
grep -A1 'name: OUTBOX_MAX_ATTEMPTS' <<<"$api_deployment" | grep -q 'value: "20"' \
  || fail "api Deployment does not set OUTBOX_MAX_ATTEMPTS"
unlimited="$(helm template "$release" "$chart_dir" "${base[@]}" --set config.outboxMaxAttempts=0 --show-only templates/configmap.yaml)"
grep -q 'OUTBOX_MAX_ATTEMPTS: "0"' <<<"$unlimited" || fail "config.outboxMaxAttempts=0 is not rendered"
zero_drain="$(helm template "$release" "$chart_dir" "${base[@]}" --set config.shutdownDrainDelay=0 --show-only templates/configmap.yaml)"
grep -q 'SHUTDOWN_DRAIN_DELAY: "0"' <<<"$zero_drain" || fail "config.shutdownDrainDelay=0 is not rendered"

# Chart vs binary: every env var cmd/api reads must be rendered into the
# Deployment when every optional value is set. The names come from the binary's
# own os.Getenv / getenv("...") / env("...") literals and from the consumer
# spec table's *_CONSUMER_GROUP literals (read through a variable), so a new
# variable in cmd/api fails this test until the chart renders it.
binary_env() {
  {
    grep -ohE '(Getenv|getenv|env)\("[A-Z][A-Z0-9_]*"' "$repo_dir"/cmd/api/*.go --exclude='*_test.go' | sed -E 's/.*\("//; s/"$//'
    grep -ohE '"[A-Z][A-Z0-9_]*_CONSUMER_GROUP"' "$repo_dir"/cmd/api/*.go --exclude='*_test.go' | tr -d '"'
  } | sort -u
}
# Word splitting is safe: the names are [A-Z0-9_] only (bash 3.2 has no mapfile).
# shellcheck disable=SC2207
api_vars=($(binary_env))
(( ${#api_vars[@]} >= 10 )) || fail "found only ${#api_vars[@]} env names in cmd/api; the scan is looking in the wrong place"
full_deployment="$(helm template "$release" "$chart_dir" "${base[@]}" "${everything[@]}" --show-only templates/deployment.yaml)"
for var in "${api_vars[@]}" DATABASE_URL MIGRATIONS_DATABASE_URL KAFKA_BROKERS DELIVERY_PROMISE_URL \
  OFFER_AVAILABILITY_CONSUMER_GROUP PRICING_CONSUMER_GROUP CHECKOUT_CONSUMER_GROUP \
  HTTP_ADDR LOG_LEVEL CART_CURRENCY ABANDON_SWEEP_INTERVAL PURGE_SWEEP_INTERVAL \
  OUTBOX_RELAY_INTERVAL OUTBOX_MAX_ATTEMPTS SHUTDOWN_DRAIN_DELAY DELIVERY_PROMISE_TIMEOUT; do
  grep -q "name: $var\$" <<<"$full_deployment" || fail "the api Deployment does not render $var, which cmd/api reads"
done

# Every key the ConfigMap renders is a variable cmd/api reads (no dead keys), and
# the ConfigMap never carries a DSN.
full_configmap="$(helm template "$release" "$chart_dir" "${base[@]}" "${everything[@]}" --show-only templates/configmap.yaml)"
api_known=" ${api_vars[*]} "
while read -r key; do
  [[ "$api_known" == *" $key "* ]] || fail "ConfigMap key $key is read by no binary"
done < <(awk '/^data:/ {d=1; next} d && /^  [A-Z]/ {sub(/:.*/, ""); sub(/^  /, ""); print}' <<<"$full_configmap")
if grep -qE '^  (MIGRATIONS_)?DATABASE_URL:' <<<"$full_configmap"; then fail "a DSN must never land in the ConfigMap"; fi

# The mandatory configuration is rendered from values.
env_value() { # env_value <rendered> <NAME>
  grep -A1 "name: $2\$" <<<"$1" | grep -m1 'value:' | sed -E 's/.*value: "?//; s/"$//'
}
[[ "$(env_value "$api_deployment" KAFKA_BROKERS)" == "kafka.example.invalid:9092" ]] || fail "KAFKA_BROKERS is not rendered from kafka.brokers"
grep -q '^  KAFKA_BROKERS: "kafka.example.invalid:9092"' <<<"$configmap" || fail "the ConfigMap does not carry KAFKA_BROKERS"
[[ "$(env_value "$api_deployment" DELIVERY_PROMISE_URL)" == "http://delivery-promise.example.invalid:8080" ]] \
  || fail "DELIVERY_PROMISE_URL is not rendered from deliveryPromise.url"
grep -q '^  DELIVERY_PROMISE_URL: "http://delivery-promise.example.invalid:8080"' <<<"$configmap" \
  || fail "the ConfigMap does not carry DELIVERY_PROMISE_URL"
for kv in 'OFFER_AVAILABILITY_CONSUMER_GROUP=shopping-cart-offer-availability' \
          'PRICING_CONSUMER_GROUP=shopping-cart-pricing' \
          'CHECKOUT_CONSUMER_GROUP=shopping-cart-checkout'; do
  [[ "$(env_value "$api_deployment" "${kv%%=*}")" == "${kv#*=}" ]] || fail "the api Deployment does not render $kv"
  grep -q "^  ${kv%%=*}: \"${kv#*=}\"" <<<"$configmap" || fail "the ConfigMap does not carry $kv"
done
# The group ids are stable: they do not change with the release name and are
# never derived from the pod name or the release.
other="$(helm template another-release "$chart_dir" "${base[@]}" --show-only templates/deployment.yaml)"
for kv in 'OFFER_AVAILABILITY_CONSUMER_GROUP=shopping-cart-offer-availability' \
          'PRICING_CONSUMER_GROUP=shopping-cart-pricing' \
          'CHECKOUT_CONSUMER_GROUP=shopping-cart-checkout'; do
  [[ "$(env_value "$other" "${kv%%=*}")" == "${kv#*=}" ]] || fail "${kv%%=*} must not depend on the release name"
done
if grep -E 'name: [A-Z_]*CONSUMER_GROUP$' -A3 <<<"$api_deployment" | grep -qE 'fieldRef|metadata.name|HOSTNAME'; then
  fail "a consumer group id must never come from the pod name"
fi

# DATABASE_URL comes from a Secret; MIGRATIONS_DATABASE_URL is optional.
grep -A4 'name: DATABASE_URL$' <<<"$api_deployment" | grep -q 'secretKeyRef:' || fail "DATABASE_URL must come from a Secret (secretKeyRef)"
grep -A6 'name: MIGRATIONS_DATABASE_URL$' <<<"$api_deployment" | grep -q 'secretKeyRef:' || fail "MIGRATIONS_DATABASE_URL must come from a Secret (secretKeyRef)"
grep -A6 'name: MIGRATIONS_DATABASE_URL$' <<<"$api_deployment" | grep -q 'optional: true' || fail "MIGRATIONS_DATABASE_URL must be optional: true"
grep -A6 'name: MIGRATIONS_DATABASE_URL$' <<<"$api_deployment" | grep -q 'key: MIGRATIONS_DATABASE_URL' \
  || fail "MIGRATIONS_DATABASE_URL must read database.migrationsExistingSecretKey"
existing="$(helm template "$release" "$chart_dir" --set database.existingSecret=my-db "${base[@]:2}" --show-only templates/deployment.yaml)"
grep -A4 'name: DATABASE_URL$' <<<"$existing" | grep -q 'name: my-db' || fail "database.existingSecret must be the DATABASE_URL Secret"
no_secret="$(helm template "$release" "$chart_dir" --set database.existingSecret=my-db "${base[@]:2}")"
if grep -q 'kind: Secret' <<<"$no_secret"; then fail "no Secret may be created when database.existingSecret is set"; fi
helm template "$release" "$chart_dir" "${base[@]}" "${everything[@]}" | grep -q 'kind: Secret' || fail "database.url must create the Secret"

# Pod hardening, probes, the image tag and shutdown room.
for want in 'runAsNonRoot: true' 'readOnlyRootFilesystem: true' 'allowPrivilegeEscalation: false' 'type: RuntimeDefault' 'path: /healthz' 'path: /readyz'; do
  grep -q -- "$want" <<<"$api_deployment" || fail "the api Deployment lacks '$want'"
done
grep -A2 'drop:' <<<"$api_deployment" | grep -q 'ALL' || fail "the api container must drop ALL capabilities"
grep -B1 -A3 'livenessProbe:' <<<"$api_deployment" | grep -q 'livenessProbe' && grep -A3 'livenessProbe:' <<<"$api_deployment" | grep -q '/healthz' \
  || fail "livenessProbe must be /healthz"
grep -A3 'readinessProbe:' <<<"$api_deployment" | grep -q '/readyz' || fail "readinessProbe must be /readyz"
grep -q 'image: "warehouse/shopping-cart:v0.1.0"' <<<"$api_deployment" \
  || fail "an empty image.tag must resolve to v<appVersion>, the tag the release job publishes"
grace="$(awk '/terminationGracePeriodSeconds:/ {print $2; exit}' <<<"$api_deployment")"
# SHUTDOWN_DRAIN_DELAY 5s + HTTP 10s + the three consumers and two sweeps 10s
# each (50s) + relay 10s = 75s.
(( grace > 75 )) || fail "terminationGracePeriodSeconds is $grace; it must exceed the binary's shutdown bounds (75s)"

# No *_MODE or in-memory/permissive wording anywhere in the render.
render_all="$(helm template "$release" "$chart_dir" "${base[@]}" "${everything[@]}")"
if grep -qE '(^|[^A-Z_])[A-Z]+_MODE\b' <<<"$render_all"; then fail "the render carries a *_MODE variable, which this service does not have"; fi
if grep -qiE 'in-memory|silently runs|permissive' <<<"$render_all"$'\n'"$(cat "$chart_dir/templates/NOTES.txt")"; then
  fail "the render or NOTES.txt mentions an in-memory / permissive mode"
fi

# Only the api component exists: one Deployment, one Service, no MCP or frontend.
for stray in mcp frontend projector reports analytics; do
  if grep -qE "app.kubernetes.io/component: .*$stray" <<<"$render_all"; then fail "the chart must not render a $stray component"; fi
done
[[ "$(grep -c '^kind: Deployment' <<<"$render_all")" == 1 ]] || fail "exactly one Deployment (the api) must render"
[[ "$(grep -c '^kind: Service$' <<<"$render_all")" == 1 ]] || fail "exactly one Service (the api) must render"
for off in HorizontalPodAutoscaler Ingress HTTPRoute; do
  if grep -q "^kind: $off" <<<"$(helm template "$release" "$chart_dir" "${base[@]}")"; then fail "$off must be off by default"; fi
  grep -q "^kind: $off" <<<"$render_all" || fail "$off must render when enabled"
done

# --- refusals: the mandatory values name themselves ---------------------------------
expect_refusal() { # expect_refusal <why> <needle> <helm args...>
  local why="$1" needle="$2" err; shift 2
  if err="$(helm template "$release" "$chart_dir" "$@" 2>&1)"; then
    fail "the chart must refuse to render: $why"
  fi
  grep -qF -- "$needle" <<<"$err" || fail "the refusal for '$why' does not name '$needle':
$err"
}
# args that satisfy everything except the one value a case removes.
mandatory_except() { # mandatory_except <database|kafka|deliveryPromise|<group key>>
  local skip="$1" out=()
  [[ "$skip" == database ]] || out+=(--set database.url=postgres://u@h/db)
  [[ "$skip" == kafka ]] || out+=(--set kafka.brokers=k:9092)
  [[ "$skip" == deliveryPromise ]] || out+=(--set deliveryPromise.url=http://dp)
  for g in "${groups[@]}"; do
    [[ "${g%%:*}" == "$skip" ]] || out+=(--set "config.${g%%:*}=${g#*:}")
  done
  printf '%s\n' "${out[@]}"
}
refusal_without() { # refusal_without <value to leave out> <needle>
  local args=()
  while IFS= read -r line; do args+=("$line"); done < <(mandatory_except "$1")
  expect_refusal "no $1" "$2" "${args[@]}"
}
refusal_without database "database.url or database.existingSecret"
refusal_without kafka "kafka.brokers"
refusal_without deliveryPromise "deliveryPromise.url"
for g in "${groups[@]}"; do refusal_without "${g%%:*}" "config.${g%%:*}"; done
expect_refusal "a *_MODE through extraEnv" "_MODE" "${base[@]}" --set 'extraEnv[0].name=CACHE_MODE' --set 'extraEnv[0].value=permissive'
expect_refusal "a *_MODE through extraEnv (off)" "_MODE" "${base[@]}" --set 'extraEnv[0].name=DEMAND_MODE' --set 'extraEnv[0].value=off'
expect_refusal "a consumer group through extraEnv" "OFFER_AVAILABILITY_CONSUMER_GROUP" "${base[@]}" --set 'extraEnv[0].name=OFFER_AVAILABILITY_CONSUMER_GROUP' --set 'extraEnv[0].value=x'
expect_refusal "a chart-owned variable through extraEnv" "DATABASE_URL" "${base[@]}" --set 'extraEnv[0].name=DATABASE_URL' --set 'extraEnv[0].value=x'
expect_refusal "a grace period under the shutdown bounds" "terminationGracePeriodSeconds" "${base[@]}" --set terminationGracePeriodSeconds=40
expect_refusal "a grace period equal to the shutdown bounds" "must exceed 75" "${base[@]}" --set terminationGracePeriodSeconds=75
expect_refusal "no grace period" "terminationGracePeriodSeconds" "${base[@]}" --set terminationGracePeriodSeconds=0
helm template "$release" "$chart_dir" "${base[@]}" --set terminationGracePeriodSeconds=76 >/dev/null \
  || fail "a 76s grace period (just above the 75s bounds) must still render"
# A longer drain delay raises the bar by the same amount.
expect_refusal "a 30s drain delay with the default grace period" "must exceed 100" "${base[@]}" --set config.shutdownDrainDelay=30s
helm template "$release" "$chart_dir" "${base[@]}" --set config.shutdownDrainDelay=30s --set terminationGracePeriodSeconds=101 >/dev/null \
  || fail "a 30s drain delay with a 101s grace period must render"
expect_refusal "a drain delay the chart cannot size" "config.shutdownDrainDelay" "${base[@]}" --set config.shutdownDrainDelay=soon
expect_refusal "an http address off the container port" "config.httpAddr" "${base[@]}" --set config.httpAddr=:9090

# The full render (ct lint's all-components values file) carries every optional
# template together, still exactly one Deployment and one Service.
together="$(helm template "$release" "$chart_dir" -f "$chart_dir/ci/all-components-values.yaml")"
grep -q 'app.kubernetes.io/component: api$' <<<"$together" || fail "the full render lacks the api component"
[[ "$(grep -c '^kind: Deployment' <<<"$together")" == 1 ]] || fail "the full render must carry exactly the api Deployment"
[[ "$(grep -c '^kind: Service$' <<<"$together")" == 1 ]] || fail "the full render must carry exactly the api Service"

echo "PASS: shopping-cart chart render assertions"
