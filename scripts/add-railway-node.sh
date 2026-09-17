#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [[ ${1:-} == "-h" || ${1:-} == "--help" ]]; then
  cat <<'EOF'
Uso: ./scripts/add-railway-node.sh

Escolhe um projeto Acervo existente, cria ou retoma o próximo nó completo e
acompanha sua sincronização e admissão no CockroachDB do cluster.
EOF
  exit 0
fi
# shellcheck source=railway-common.sh
source "$SCRIPT_DIR/railway-common.sh"

cd "$PROJECT_ROOT"

require_commands
choose_existing_project
refresh_services

LAST_INDEX=$(existing_node_count)
LAST_STATE=$(cluster_node_state "$LAST_INDEX")
LAST_DEPLOYMENT=""
if service_exists "backend-node-$LAST_INDEX"; then
  LAST_DEPLOYMENT=$(deployment_id "backend-node-$LAST_INDEX")
fi
if [[ -z $LAST_DEPLOYMENT || -z $LAST_STATE || $LAST_STATE == joining || $LAST_STATE == syncing ]]; then
  NEW_INDEX=$LAST_INDEX
  printf '\nO backend-node-%d está incompleto. O script retomará sua criação.\n' "$NEW_INDEX"
else
  NEW_INDEX=$((LAST_INDEX + 1))
  printf '\nSerá criado o backend-node-%d com CockroachDB e MinIO próprios.\n' "$NEW_INDEX"
fi
confirm "Adicionar o novo nó?" || exit 0

load_or_create_secrets
ensure_service "backend-node-$NEW_INDEX" railway/Dockerfile.backend
ensure_service "cockroach-$NEW_INDEX" railway/Dockerfile.cockroach
ensure_service "minio-$NEW_INDEX" railway/Dockerfile.minio
ensure_volume "cockroach-$NEW_INDEX" /cockroach/cockroach-data
ensure_volume "minio-$NEW_INDEX" /data
refresh_services
ensure_ssh_material
mapfile -t ACTIVE_INDICES < <(active_node_indices)
TOPOLOGY_INDICES=("${ACTIVE_INDICES[@]}")
if [[ ! " ${TOPOLOGY_INDICES[*]} " =~ " $NEW_INDEX " ]]; then
  TOPOLOGY_INDICES+=("$NEW_INDEX")
fi
configure_stack "${TOPOLOGY_INDICES[@]}"

deploy_service "cockroach-$NEW_INDEX"
deploy_service "minio-$NEW_INDEX"
initialize_storage "${TOPOLOGY_INDICES[@]}"
deploy_service fault-actuator backend
deploy_service "backend-node-$NEW_INDEX"
reconcile_storage_identities "$NEW_INDEX"
deploy_service load-balancer
observe_node_admission "backend-node-$NEW_INDEX"

DOMAIN=$(public_domain)
printf '\n\033[1;32mNó adicionado\033[0m\n'
printf 'Novo nó: backend-node-%d\n' "$NEW_INDEX"
printf 'Domínio: %s\n' "$DOMAIN"
