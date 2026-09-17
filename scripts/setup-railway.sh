#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [[ ${1:-} == "-h" || ${1:-} == "--help" ]]; then
  cat <<'EOF'
Uso: ./scripts/setup-railway.sh

Cria ou reutiliza um projeto Railway, pergunta a quantidade de nós e configura,
implanta e publica toda a topologia do Acervo sem depender do painel web.
EOF
  exit 0
fi
# shellcheck source=railway-common.sh
source "$SCRIPT_DIR/railway-common.sh"

cd "$PROJECT_ROOT"

require_commands
choose_or_create_project
refresh_services

printf '\nA topologia usa um backend, um CockroachDB e um MinIO por nó.\n'
NODE_COUNT=$(prompt_number "Quantos nós deseja criar?" 3)
if ((NODE_COUNT < 3)); then
  warn "com menos de 3 nós, a demonstração não mantém quorum após uma falha"
  confirm "Deseja continuar mesmo assim?" || exit 0
fi

load_or_create_secrets
node_services "$NODE_COUNT"
refresh_services
ensure_ssh_material
NODE_INDICES=()
for ((i=1; i<=NODE_COUNT; i++)); do NODE_INDICES+=("$i"); done
configure_stack "${NODE_INDICES[@]}"

printf '\nO script vai implantar %d nós e os serviços de entrada e atuação.\n' "$NODE_COUNT"
confirm "Iniciar os deploys agora?" || { warn "configuração salva; execute o script novamente para continuar"; exit 0; }

deploy_full_stack "$NODE_COUNT"
DOMAIN=$(public_domain)
show_credentials_and_domain "$DOMAIN"
