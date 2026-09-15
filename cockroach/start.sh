#!/bin/sh
set -eu

: "${COCKROACH_ADVERTISE_ADDR:?COCKROACH_ADVERTISE_ADDR nao definido}"
: "${COCKROACH_JOIN:?Defina COCKROACH_JOIN com um ou mais nos iniciais}"

exec /cockroach/cockroach start \
    --insecure \
    --listen-addr="[::]:26257" \
    --http-addr="[::]:${PORT:-8080}" \
    --advertise-addr="$COCKROACH_ADVERTISE_ADDR" \
    --join="$COCKROACH_JOIN" \
    --store=/cockroach/cockroach-data
