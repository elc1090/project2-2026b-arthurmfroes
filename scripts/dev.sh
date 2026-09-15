#!/bin/sh
set -eu

project_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"

exec docker compose \
    --file "$project_root/docker-compose.dev.yml" \
    up \
    --build \
    "$@"
