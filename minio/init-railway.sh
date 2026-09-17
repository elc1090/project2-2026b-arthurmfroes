#!/bin/sh
set -eu

node_count=$#
[ "$node_count" -ge 2 ] || { echo "Informe ao menos dois índices de sites MinIO." >&2; exit 1; }

site_indices=""
for site_index do
    case "$site_index" in
        *[!0-9]*|'') echo "Índice de site MinIO inválido: $site_index." >&2; exit 1 ;;
    esac
    [ "$site_index" -gt 0 ] || { echo "Índice de site MinIO inválido: $site_index." >&2; exit 1; }
    case " $site_indices " in
        *" $site_index "*) echo "Índice de site MinIO repetido: $site_index." >&2; exit 1 ;;
    esac
    site_indices="$site_indices${site_indices:+ }$site_index"
    new_site=$site_index
done

access_key=${MINIO_ROOT_USER:?MINIO_ROOT_USER não definido}
secret_key=${MINIO_ROOT_PASSWORD:?MINIO_ROOT_PASSWORD não definido}

aliases=""
primary_alias=""
for i in $site_indices; do
    alias_name="minio-$i"
    [ -n "$primary_alias" ] || primary_alias=$alias_name
    endpoint="http://minio-$i.railway.internal:9000"
    attempt=1
    until mc alias set "$alias_name" "$endpoint" "$access_key" "$secret_key" >/dev/null 2>&1; do
        [ "$attempt" -lt 30 ] || { echo "MinIO $alias_name não ficou acessível." >&2; exit 1; }
        attempt=$((attempt + 1))
        sleep 2
    done
    aliases="$aliases $alias_name"
done

replication_count() {
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
    peers=0
    while IFS='|' read -r deployment name endpoint rest; do
        endpoint=${endpoint#"${endpoint%%[![:space:]]*}"}
        endpoint=${endpoint%"${endpoint##*[![:space:]]}"}
        case "$endpoint" in ''|Endpoint) continue ;; esac
        valid=false
        for expected in $site_indices; do
            if [ "$endpoint" = "http://minio-$expected.railway.internal:9000" ]; then
                valid=true
                break
            fi
        done
        [ "$valid" = true ] || return 1
        case "$seen" in *" $endpoint "*) return 1 ;; esac
        seen="$seen$endpoint "
        peers=$((peers + 1))
    done <<EOF
$info
EOF
    [ "$peers" -gt 0 ] || return 1
    echo "$peers"
}

disabled=0
complete=0
expandable=0
for i in $site_indices; do
    state=$(replication_count "minio-$i") || {
        echo "Não foi possível validar a replicação em minio-$i." >&2
        exit 1
    }
    if [ "$state" = disabled ]; then
        disabled=$((disabled + 1))
    elif [ "$state" -eq "$node_count" ]; then
        complete=$((complete + 1))
    elif [ "$i" -ne "$new_site" ] && [ "$state" -eq $((node_count - 1)) ]; then
        expandable=$((expandable + 1))
    else
        echo "Topologia de replicação inesperada em minio-$i: $state peers." >&2
        exit 1
    fi
done

if [ "$disabled" -eq "$node_count" ]; then
    # Todos os sites estão vazios; a ordem não altera a origem.
    # shellcheck disable=SC2086
    mc admin replicate add $aliases
elif [ "$complete" -eq "$node_count" ]; then
    :
elif [ "$disabled" -eq 1 ] && [ "$expandable" -eq $((node_count - 1)) ]; then
    [ "$(replication_count "minio-$new_site")" = disabled ] || {
        echo "Somente o último site pode estar vazio durante uma expansão." >&2
        exit 1
    }
    # A CLI exige os peers existentes seguidos pelo novo site vazio.
    # shellcheck disable=SC2086
    mc admin replicate add $aliases
else
    echo "Configuração MinIO mista; nenhuma alteração foi aplicada." >&2
    exit 1
fi

attempt=1
while :; do
    verified=0
    for i in $site_indices; do
        state=$(replication_count "minio-$i" 2>/dev/null || echo invalid)
        [ "$state" = "$node_count" ] && verified=$((verified + 1))
    done
    [ "$verified" -eq "$node_count" ] && break
    [ "$attempt" -lt 30 ] || { echo "Replicação MinIO não convergiu em todos os sites." >&2; exit 1; }
    attempt=$((attempt + 1))
    sleep 2
done

mc mb --ignore-existing "$primary_alias/drive-clone" >/dev/null
for i in $site_indices; do
    mc stat "minio-$i/drive-clone" >/dev/null
    version_state=$(mc version info "minio-$i/drive-clone")
    case "$version_state" in
        *enabled*|*Enabled*) ;;
        *) echo "Versionamento não está ativo em minio-$i/drive-clone." >&2; exit 1 ;;
    esac
done
echo "Replicação e bucket MinIO verificados em $node_count sites."
