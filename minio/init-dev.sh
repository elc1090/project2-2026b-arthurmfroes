#!/bin/sh
set -eu

configure_alias() {
    alias_name="$1"
    endpoint="$2"

    mc alias set "$alias_name" "$endpoint" minioadmin minioadmin
}

configure_alias minio-1 http://minio-1:9000
configure_alias minio-2 http://minio-2:9000
configure_alias minio-3 http://minio-3:9000

# mc returns success even when replication is disabled. Inspect every site's
# table, including the endpoint column, instead of trusting the exit status.
# --no-color keeps parsing independent of terminal escape sequences. Unknown
# output formats are an error, never evidence that replication is configured.
replication_state() {
    info=$(mc --no-color admin replicate info "$1") || return 1
    if [ "$info" = 'SiteReplication is not enabled' ]; then
        echo disabled
        return
    fi
    case "$info" in
        'SiteReplication enabled for:'*) ;;
        *) return 1 ;;
    esac
    seen=' '
    count=0
    while IFS='|' read -r deployment name endpoint rest; do
        # Trim table padding using shell builtins; the mc image has no awk/jq.
        endpoint=${endpoint#"${endpoint%%[![:space:]]*}"}
        endpoint=${endpoint%"${endpoint##*[![:space:]]}"}
        case "$endpoint" in
            ''|Endpoint) continue ;;
            http://minio-1:9000|http://minio-2:9000|http://minio-3:9000) ;;
            *) return 1 ;;
        esac
        case "$seen" in *" $endpoint "*) return 1 ;; esac
        seen="$seen$endpoint "
        count=$((count + 1))
    done <<EOF
$info
EOF
    [ "$count" -eq 3 ] || return 1
    echo complete
}

disabled=0
for site in minio-1 minio-2 minio-3; do
    state=$(replication_state "$site") || {
        echo "Cannot confirm the three expected replication peers on $site. No topology changed." >&2
        exit 1
    }
    if [ "$state" = disabled ]; then disabled=$((disabled + 1)); fi
done

if [ "$disabled" -eq 3 ]; then
    mc admin replicate add minio-1 minio-2 minio-3
elif [ "$disabled" -ne 0 ]; then
    echo "Mixed MinIO replication configuration; inspect all sites before retrying. No topology changed." >&2
    exit 1
fi

# Confirm what add actually persisted, and also check all peers on warm starts.
for site in minio-1 minio-2 minio-3; do
    state=$(replication_state "$site") || exit 1
    if [ "$state" != complete ]; then
        echo "Replication still incomplete on $site." >&2
        exit 1
    fi
done

mc mb --ignore-existing minio-1/drive-clone
for site in minio-1 minio-2 minio-3; do
    mc stat "$site/drive-clone"
done
echo "MinIO peers and bucket verified on all three sites."
