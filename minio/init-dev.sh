#!/bin/sh
set -eu

configure_alias() {
    alias_name="$1"
    endpoint="$2"

    until mc alias set "$alias_name" "$endpoint" minioadmin minioadmin; do
        sleep 2
    done
}

configure_alias minio-1 http://minio-1:9000
configure_alias minio-2 http://minio-2:9000
configure_alias minio-3 http://minio-3:9000

if ! mc admin replicate info minio-1 >/dev/null 2>&1; then
    mc admin replicate add minio-1 minio-2 minio-3
fi

mc mb --ignore-existing minio-1/drive-clone
