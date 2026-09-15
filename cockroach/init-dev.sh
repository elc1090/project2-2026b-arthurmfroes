#!/bin/sh
set -eu

cluster_host="cockroach-1:26257"

until cockroach init --insecure --host="$cluster_host"; do
    if cockroach sql --insecure --host="$cluster_host" --execute="SELECT 1" >/dev/null 2>&1; then
        break
    fi

    sleep 2
done

cockroach sql \
    --insecure \
    --host="$cluster_host" \
    --execute="CREATE DATABASE IF NOT EXISTS drive_clone"
