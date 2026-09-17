#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [[ ${1:-} == "-h" || ${1:-} == "--help" ]]; then
  cat <<'EOF'
Uso: ./scripts/add-railway-node.sh

Escolhe um projeto Acervo existente, cria o próximo nó completo e acompanha sua
sincronização e admissão consultando diretamente o CockroachDB do cluster.
EOF
  exit 0
fi
# shellcheck source=railway-common.sh
source "$SCRIPT_DIR/railway-common.sh"

cd "$PROJECT_ROOT"

require_commands
choose_existing_project
refresh_services

CURRENT_COUNT=$(existing_node_count)
NEW_INDEX=$((CURRENT_COUNT + 1))
printf '\nO projeto possui %d nós. Será criado o backend-node-%d com CockroachDB e MinIO próprios.\n' "$CURRENT_COUNT" "$NEW_INDEX"
confirm "Adicionar o novo nó?" || exit 0

load_or_create_secrets
ensure_service "backend-node-$NEW_INDEX" railway/Dockerfile.backend
ensure_service "cockroach-$NEW_INDEX" railway/Dockerfile.cockroach
ensure_service "minio-$NEW_INDEX" railway/Dockerfile.minio
ensure_volume "cockroach-$NEW_INDEX" /cockroach/cockroach-data
ensure_volume "minio-$NEW_INDEX" /data
refresh_services
ensure_ssh_material
configure_stack "$NEW_INDEX"

deploy_service "cockroach-$NEW_INDEX"
deploy_service "minio-$NEW_INDEX"
deploy_service fault-actuator backend
deploy_service "backend-node-$NEW_INDEX"
deploy_service load-balancer
observe_node_admission "backend-node-$NEW_INDEX"

DOMAIN=$(public_domain)
printf '\n\033[1;32mNó adicionado\033[0m\n'
printf 'Novo nó: backend-node-%d\n' "$NEW_INDEX"
printf 'Domínio: %s\n' "$DOMAIN"
