#!/bin/sh
set -eu

# Probe every SQL gateway first. A preserved cluster can have node 1 unavailable.
for cluster_host in cockroach-1:26257 cockroach-2:26257 cockroach-3:26257; do
    if cockroach sql \
        --url="postgresql://root@$cluster_host/defaultdb?sslmode=disable&connect_timeout=5" \
        --execute="SET statement_timeout = '5s'; CREATE DATABASE IF NOT EXISTS drive_clone"; then
        echo "Existing CockroachDB cluster available through $cluster_host."
        exit 0
    fi
done

# init is rejected by an already initialized node. Never reset or replace a store
# when probes fail: the cluster may simply be starting or lack quorum.
cockroach init --insecure --host=cockroach-1:26257 || {
    echo "CockroachDB bootstrap not confirmed; retry after checking quorum and logs." >&2
    exit 1
}

cockroach sql --insecure --host=cockroach-1:26257 \
    --execute="CREATE DATABASE IF NOT EXISTS drive_clone"
