#!/bin/bash
#
# Clone puckdb_prod (Hollingsworth cluster) into the local docker-compose
# postgres. Drops/recreates local 'puckdb' DB before restoring.
#
# Strategy:
#   1. Run pg_dump in-cluster as a one-shot pod, writing to the
#      puckdb-juicefs PVC. (Avoids kubectl port-forward's chatty-protocol
#      throughput cliff.)
#   2. Same pod then serves the dump file over HTTP via python's stdlib
#      http.server. (kubectl cp and kubectl exec cat both have silent
#      stdout-truncation issues over the websocket layer; HTTP gives us
#      Content-Length validation and curl --fail catches partial reads.)
#   3. DROP DATABASE WITH (FORCE) + CREATE locally, then pg_restore into
#      the empty DB. (--clean is not enough: it issues DROP TABLE without
#      CASCADE, which silently fails on FK-fanout parents like 'seasons'
#      and 'players'.)
#   4. pg_restore runs inside the docker `db` container so it bypasses
#      pgbouncer (transaction-pool mode breaks pg_restore's multi-stmt
#      DDL).

set -euo pipefail

NS=puckdb
PVC=puckdb-juicefs
DUMP_POD=puckdb-prod-dump
DUMP_PATH_IN_POD=/mnt/puckdb-data/dumps/puckdb_prod.dump
LOCAL_DB=puckdb
LOCAL_HTTP_PORT=18080
DUMP_FILE="${TMPDIR:-/tmp}/puckdb_prod.dump"

# Preflight: tools present, docker db up.
command -v kubectl >/dev/null || { echo "kubectl not found"; exit 1; }
command -v docker >/dev/null  || { echo "docker not found"; exit 1; }
command -v curl >/dev/null    || { echo "curl not found"; exit 1; }

if ! docker compose ps db --status running --quiet | grep -q .; then
    echo "local 'db' service is not running — start docker compose first"
    exit 1
fi

# Clean up leftover pod from a prior run.
kubectl delete pod -n "$NS" "$DUMP_POD" --ignore-not-found --wait=true >/dev/null

# On failure, leave the pod alive so it can be inspected via
# `kubectl logs -n puckdb $DUMP_POD` or `kubectl describe ...`. On
# success, the explicit cleanup near the end deletes it. The trap
# only kills the port-forward.
trap 'kill ${PF_PID:-} 2>/dev/null || true' EXIT
on_failure() {
    echo
    echo "FAILED — leaving pod $NS/$DUMP_POD for inspection."
    echo "  kubectl logs -n $NS $DUMP_POD"
    echo "  kubectl describe pod -n $NS $DUMP_POD"
    echo "  kubectl delete pod -n $NS $DUMP_POD   # when done"
    exit 1
}
trap on_failure ERR

echo "Launching in-cluster dump pod ($NS/$DUMP_POD)"
# initContainer 'dump' runs pg_dump (postgres:16-alpine has the client).
# Main container 'serve' starts only after dump succeeds, runs python's
# http.server on the shared JuiceFS mount. python:3.12-alpine has
# stdlib http.server with no install needed at runtime.
kubectl apply -f - >/dev/null <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: $DUMP_POD
  namespace: $NS
spec:
  restartPolicy: Never
  securityContext:
    runAsUser: 1026
    runAsGroup: 100
    fsGroup: 100
  initContainers:
  - name: dump
    image: postgres:16-alpine
    command: ["sh", "-c"]
    args:
      - |
        set -e
        mkdir -p \$(dirname $DUMP_PATH_IN_POD)
        echo "starting pg_dump..."
        pg_dump -h shared-postgres-rw.postgres \\
          -U puckdb -d puckdb_prod \\
          -Fc --verbose \\
          -f $DUMP_PATH_IN_POD
        echo "DUMP_DONE size=\$(du -h $DUMP_PATH_IN_POD | cut -f1)"
    env:
    - name: PGPASSWORD
      valueFrom:
        secretKeyRef:
          name: puckdb-db-credentials
          key: password
    volumeMounts:
    - name: puckdb-data
      mountPath: /mnt/puckdb-data
  containers:
  - name: serve
    image: python:3.12-alpine
    command: ["python", "-m", "http.server", "8080", "-d"]
    args: ["$(dirname $DUMP_PATH_IN_POD)"]
    ports:
    - containerPort: 8080
    volumeMounts:
    - name: puckdb-data
      mountPath: /mnt/puckdb-data
      readOnly: true
  volumes:
  - name: puckdb-data
    persistentVolumeClaim:
      claimName: $PVC
EOF

echo "Waiting for pod to be PodScheduled..."
kubectl wait pod/"$DUMP_POD" -n "$NS" --for=condition=PodScheduled --timeout=60s >/dev/null

echo "Streaming pg_dump verbose output (initContainer logs):"
# kubectl logs -f on an initContainer blocks until that container exits.
# When the stream ends, dump is complete (or failed).
kubectl logs -n "$NS" -c dump -f "$DUMP_POD" 2>&1 || true

# Verify dump initContainer succeeded.
init_exit=$(kubectl get pod -n "$NS" "$DUMP_POD" \
    -o jsonpath='{.status.initContainerStatuses[?(@.name=="dump")].state.terminated.exitCode}')
if [[ "$init_exit" != "0" ]]; then
    echo "pg_dump initContainer exited with code $init_exit"
    exit 1
fi

echo "Waiting for HTTP server to be Ready..."
kubectl wait pod/"$DUMP_POD" -n "$NS" --for=condition=Ready --timeout=60s >/dev/null

# Port-forward the http.server and download with integrity check.
echo "Port-forwarding HTTP server -> localhost:$LOCAL_HTTP_PORT"
kubectl port-forward -n "$NS" "pod/$DUMP_POD" "$LOCAL_HTTP_PORT:8080" >/dev/null 2>&1 &
PF_PID=$!

# Wait for the forward's listening socket.
for i in {1..15}; do
    if (exec 3<>/dev/tcp/localhost/"$LOCAL_HTTP_PORT") 2>/dev/null; then
        exec 3>&- 3<&-
        break
    fi
    if (( i == 15 )); then
        echo "port-forward never became ready"
        exit 1
    fi
    sleep 1
done

rm -f "$DUMP_FILE"
echo "Downloading dump -> $DUMP_FILE"
curl --fail --output "$DUMP_FILE" \
    "http://localhost:$LOCAL_HTTP_PORT/$(basename $DUMP_PATH_IN_POD)"

local_size=$(stat -f%z "$DUMP_FILE" 2>/dev/null || stat -c%s "$DUMP_FILE")
pod_size=$(kubectl exec -n "$NS" "$DUMP_POD" -c serve -- \
    stat -c%s "$DUMP_PATH_IN_POD")
if [[ "$local_size" != "$pod_size" ]]; then
    echo "size mismatch: local=$local_size pod=$pod_size — re-run script"
    exit 1
fi
echo "Local dump size: $(du -h "$DUMP_FILE" | cut -f1) (byte-exact)"

# Drop port-forward early; the trap will also handle it.
kill $PF_PID 2>/dev/null || true
unset PF_PID

# Drop and recreate local DB. WITH (FORCE) boots active sessions
# (pgbouncer reconnect loop) atomically. Multi-statement -c can't be
# used because DROP DATABASE refuses to run inside a transaction block.
echo "Dropping + recreating local '$LOCAL_DB' database"
docker compose exec -T db psql -U puckdb -d postgres -c \
    "DROP DATABASE IF EXISTS $LOCAL_DB WITH (FORCE)"
docker compose exec -T db psql -U puckdb -d postgres -c \
    "CREATE DATABASE $LOCAL_DB"

# Restore inside the db container so we bypass pgbouncer.
echo "Copying dump into docker db container"
docker compose cp "$DUMP_FILE" db:/tmp/puckdb_prod.dump

echo "Restoring into '$LOCAL_DB' (-j 4)"
docker compose exec -T db pg_restore \
    -U puckdb -d "$LOCAL_DB" \
    --no-owner --no-acl \
    -j 4 \
    /tmp/puckdb_prod.dump

# Success: clean up the pod that the ERR trap would have preserved.
kubectl delete pod -n "$NS" "$DUMP_POD" --ignore-not-found --wait=false >/dev/null 2>&1 || true

# Sanity check.
echo
echo "Local row counts (top 10):"
docker compose exec -T db psql -U puckdb -d "$LOCAL_DB" -At -c \
    "SELECT relname || ' = ' || n_live_tup
     FROM pg_stat_user_tables
     WHERE n_live_tup > 0
     ORDER BY n_live_tup DESC LIMIT 10"
