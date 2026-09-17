#!/usr/bin/env bash

# Funções compartilhadas pelos roteiros interativos do Railway.
# Este arquivo deve ser carregado com source; não o execute diretamente.

set -euo pipefail

PROJECT_ROOT=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
RAILWAY_LOCAL_DIR="$PROJECT_ROOT/.railway-local"
RAILWAY_ENVIRONMENT_ID=""
RAILWAY_PROJECT_ID=""
RAILWAY_PROJECT_NAME=""
SERVICES_JSON='[]'

log() { printf '\n\033[1;34m==>\033[0m %s\n' "$*"; }
ok() { printf '\033[1;32m✓\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!\033[0m %s\n' "$*"; }
die() { printf '\033[1;31mErro:\033[0m %s\n' "$*" >&2; exit 1; }

require_commands() {
  local command
  for command in railway jq openssl ssh-keygen ssh-keyscan; do
    command -v "$command" >/dev/null 2>&1 || die "comando obrigatório ausente: $command"
  done
  railway whoami >/dev/null 2>&1 || {
    log "Autenticação no Railway"
    railway login
  }
}

prompt_nonempty() {
  local prompt=$1 value=""
  while [[ -z $value ]]; do
    read -r -p "$prompt" value
  done
  printf '%s' "$value"
}

prompt_number() {
  local prompt=$1 default=$2 value=""
  while true; do
    read -r -p "$prompt [$default]: " value
    value=${value:-$default}
    [[ $value =~ ^[1-9][0-9]*$ ]] && { printf '%s' "$value"; return; }
    warn "informe um número inteiro maior que zero"
  done
}

confirm() {
  local prompt=$1 answer
  read -r -p "$prompt [s/N]: " answer
  [[ $answer == "s" || $answer == "S" ]]
}

choose_existing_project() {
  local projects entries choice index id name envs env_choice env_index env_id env_name
  projects=$(railway list --json)
  mapfile -t entries < <(jq -r '.[] | select(.deletedAt == null) | [.id, .name] | @tsv' <<<"$projects")
  ((${#entries[@]} > 0)) || die "nenhum projeto Railway encontrado nesta conta"

  printf '\nProjetos disponíveis:\n'
  for index in "${!entries[@]}"; do
    IFS=$'\t' read -r id name <<<"${entries[$index]}"
    printf '  %d) %s\n' "$((index + 1))" "$name"
  done
  while true; do
    read -r -p "Escolha o projeto: " choice
    [[ $choice =~ ^[0-9]+$ ]] && ((choice >= 1 && choice <= ${#entries[@]})) && break
    warn "opção inválida"
  done
  IFS=$'\t' read -r RAILWAY_PROJECT_ID RAILWAY_PROJECT_NAME <<<"${entries[$((choice - 1))]}"

  mapfile -t envs < <(jq -r --arg id "$RAILWAY_PROJECT_ID" '.[] | select(.id == $id) | .environments.edges[].node | select(.canAccess and .deletedAt == null) | [.id, .name] | @tsv' <<<"$projects")
  ((${#envs[@]} > 0)) || die "o projeto não possui ambiente acessível"
  if ((${#envs[@]} == 1)); then
    IFS=$'\t' read -r RAILWAY_ENVIRONMENT_ID env_name <<<"${envs[0]}"
  else
    printf '\nAmbientes disponíveis:\n'
    for env_index in "${!envs[@]}"; do
      IFS=$'\t' read -r env_id env_name <<<"${envs[$env_index]}"
      printf '  %d) %s\n' "$((env_index + 1))" "$env_name"
    done
    while true; do
      read -r -p "Escolha o ambiente: " env_choice
      [[ $env_choice =~ ^[0-9]+$ ]] && ((env_choice >= 1 && env_choice <= ${#envs[@]})) && break
      warn "opção inválida"
    done
    IFS=$'\t' read -r RAILWAY_ENVIRONMENT_ID env_name <<<"${envs[$((env_choice - 1))]}"
  fi

  railway link --project "$RAILWAY_PROJECT_ID" --environment "$RAILWAY_ENVIRONMENT_ID" --json >/dev/null
  ok "projeto $RAILWAY_PROJECT_NAME, ambiente $env_name"
}

choose_or_create_project() {
  local choice name result status
  printf '\nOnde deseja implantar?\n  1) Criar projeto novo\n  2) Usar projeto existente\n'
  while true; do
    read -r -p "Escolha uma opção: " choice
    case $choice in
      1)
        name=$(prompt_nonempty "Nome do novo projeto: ")
        result=$(railway init --name "$name" --json)
        RAILWAY_PROJECT_ID=$(jq -r '.id // .projectId // empty' <<<"$result")
        status=$(railway status --json)
        RAILWAY_PROJECT_ID=${RAILWAY_PROJECT_ID:-$(jq -r '.id // .projectId // empty' <<<"$status")}
        RAILWAY_PROJECT_NAME=$name
        RAILWAY_ENVIRONMENT_ID=$(jq -r '
          .environmentId //
          ([.environments.edges[].node | select(.deletedAt == null and .canAccess) | .id][0]) //
          empty
        ' <<<"$status")
        [[ -n $RAILWAY_PROJECT_ID && -n $RAILWAY_ENVIRONMENT_ID ]] || die "o projeto foi criado, mas a CLI não informou o projeto ou ambiente vinculado"
        ok "projeto $name criado"
        return
        ;;
      2) choose_existing_project; return ;;
      *) warn "opção inválida" ;;
    esac
  done
}

refresh_services() { SERVICES_JSON=$(railway service list --json); }

service_exists() {
  jq -e --arg name "$1" 'any(.[]; .name == $name)' <<<"$SERVICES_JSON" >/dev/null
}

service_id() {
  jq -r --arg name "$1" '.[] | select(.name == $name) | .id' <<<"$SERVICES_JSON" | head -n1
}

set_variable() {
  local service=$1 key=$2 value=$3
  printf '%s' "$value" | railway variable set "$key" --stdin --service "$service" --skip-deploys >/dev/null
}

get_variable() {
  local service=$1 key=$2
  railway variable list --service "$service" --json 2>/dev/null | jq -r --arg key "$key" '.[$key] // empty'
}

ensure_service() {
  local name=$1 dockerfile=$2
  if service_exists "$name"; then
    ok "serviço $name já existe"
  else
    log "Criando serviço $name"
    if ! railway add --service "$name" --variables "RAILWAY_DOCKERFILE_PATH=$dockerfile" --json >/dev/null; then
      die "não foi possível criar $name. Confira o plano e os limites da conta."
    fi
    refresh_services
  fi
  set_variable "$name" RAILWAY_DOCKERFILE_PATH "$dockerfile"
}

ensure_volume() {
  local service=$1 mount=$2 sid existing_mount
  refresh_services
  if jq -e --arg service "$service" '.[] | select(.name == $service) | (.volumes // []) | length > 0' <<<"$SERVICES_JSON" >/dev/null; then
    existing_mount=$(jq -r --arg service "$service" '.[] | select(.name == $service) | .volumes[0].mountPath // empty' <<<"$SERVICES_JSON")
    [[ $existing_mount == "$mount" ]] || die "o volume de $service está montado em ${existing_mount:-um caminho desconhecido}; esperado: $mount"
    ok "volume de $service já existe em $mount"
  else
    sid=$(service_id "$service")
    [[ -n $sid ]] || die "serviço $service não encontrado para criar o volume"
    log "Criando volume de $service em $mount"
    railway service link "$sid" >/dev/null
    railway volume add --mount-path "$mount" --json >/dev/null
    refresh_services
  fi
}

load_or_create_secrets() {
  local state_file existing
  mkdir -p "$RAILWAY_LOCAL_DIR"
  chmod 700 "$RAILWAY_LOCAL_DIR"
  state_file="$RAILWAY_LOCAL_DIR/$RAILWAY_PROJECT_ID.env"
  if [[ -f $state_file ]]; then
    # O arquivo contém apenas valores hexadecimais gerados por este script.
    # shellcheck disable=SC1090
    source "$state_file"
    ok "segredos locais reaproveitados"
    return
  fi

  existing=""
  if service_exists backend-node-1; then
    existing=$(get_variable backend-node-1 CONTROL_TOKEN)
  fi
  CONTROL_TOKEN=${existing:-$(openssl rand -hex 32)}
  existing=$(service_exists backend-node-1 && get_variable backend-node-1 FAULT_ACTUATOR_TOKEN || true)
  FAULT_ACTUATOR_TOKEN=${existing:-$(openssl rand -hex 32)}
  existing=$(service_exists backend-node-1 && get_variable backend-node-1 S3_SECRET_KEY || true)
  S3_SECRET_KEY=${existing:-$(openssl rand -hex 32)}
  existing=$(service_exists backend-node-1 && get_variable backend-node-1 ADMIN_PASSWORD || true)
  ADMIN_PASSWORD=${existing:-$(openssl rand -hex 24)}
  S3_ACCESS_KEY=${S3_ACCESS_KEY:-acervo}
  ADMIN_LOGIN=${ADMIN_LOGIN:-admin}

  umask 077
  cat >"$state_file" <<EOF
CONTROL_TOKEN=$CONTROL_TOKEN
FAULT_ACTUATOR_TOKEN=$FAULT_ACTUATOR_TOKEN
S3_ACCESS_KEY=$S3_ACCESS_KEY
S3_SECRET_KEY=$S3_SECRET_KEY
ADMIN_LOGIN=$ADMIN_LOGIN
ADMIN_PASSWORD=$ADMIN_PASSWORD
EOF
  chmod 600 "$state_file"
  ok "segredos gerados em .railway-local/$RAILWAY_PROJECT_ID.env"
}

ensure_ssh_material() {
  local key="$HOME/.ssh/acervo_railway_fault" known="$HOME/.ssh/acervo_railway_known_hosts" fingerprint registered private existing_known
  mkdir -p "$HOME/.ssh"
  chmod 700 "$HOME/.ssh"

  private=$(service_exists fault-actuator && get_variable fault-actuator RAILWAY_SSH_PRIVATE_KEY || true)
  existing_known=$(service_exists fault-actuator && get_variable fault-actuator RAILWAY_SSH_KNOWN_HOSTS || true)
  if [[ ! -f $key && -z $private ]]; then
    log "Gerando chave SSH exclusiva do atuador"
    ssh-keygen -q -t ed25519 -N '' -f "$key" -C acervo-railway-fault
  fi
  if [[ -f $key ]]; then
    fingerprint=$(ssh-keygen -lf "$key.pub" | awk '{print $2}')
    registered=$(railway ssh keys list 2>/dev/null || true)
    if ! grep -Fq "$fingerprint" <<<"$registered"; then
      railway ssh keys add --key "$key.pub" --name "acervo-fault-${RAILWAY_PROJECT_ID:0:8}" >/dev/null
      ok "chave pública cadastrada no Railway"
    else
      ok "chave pública já cadastrada"
    fi
    RAILWAY_SSH_PRIVATE_KEY=$(cat "$key")
  else
    RAILWAY_SSH_PRIVATE_KEY=$private
  fi

  if [[ ! -f $known && -z $existing_known ]]; then
    log "Capturando a chave apresentada por ssh.railway.com"
    ssh-keyscan -t ed25519 ssh.railway.com >"$known" 2>/dev/null || die "não foi possível consultar ssh.railway.com"
    chmod 600 "$known"
    ssh-keygen -lf "$known"
    confirm "Você reconhece e aceita esta fingerprint para o gateway Railway?" || die "fingerprint não aprovada"
  fi
  if [[ -f $known ]]; then
    RAILWAY_SSH_KNOWN_HOSTS=$(cat "$known")
  else
    RAILWAY_SSH_KNOWN_HOSTS=$existing_known
  fi
}

node_services() {
  local count=$1 i
  ensure_service load-balancer nginx/Dockerfile
  ensure_service fault-actuator Dockerfile.actuator
  for ((i=1; i<=count; i++)); do
    ensure_service "backend-node-$i" railway/Dockerfile.backend
    ensure_service "cockroach-$i" railway/Dockerfile.cockroach
    ensure_service "minio-$i" railway/Dockerfile.minio
    ensure_volume "cockroach-$i" /cockroach/cockroach-data
    ensure_volume "minio-$i" /data
  done
}

instance_id() {
  local service=$1 status
  status=$(railway status --json)
  jq -r --arg environment "$RAILWAY_ENVIRONMENT_ID" --arg service "$service" '
    .environments.edges[].node |
    select(.id == $environment) |
    .serviceInstances.edges[].node |
    select(.serviceName == $service) |
    .id
  ' <<<"$status" | head -n1
}

wait_instance_id() {
  local service=$1 attempt id
  for attempt in {1..30}; do
    id=$(instance_id "$service")
    if [[ -n $id ]]; then
      printf '%s' "$id"
      return
    fi
    sleep 2
  done
  die "a instância Railway de $service não ficou disponível após 60 segundos"
}

configure_stack() {
  local count=$1 i join endpoints targets backend_instance sql_instance storage_instance
  join=""
  endpoints=""
  targets='[]'
  for ((i=1; i<=count; i++)); do
    join+="${join:+,}cockroach-$i.railway.internal:26257"
    endpoints+="${endpoints:+ }http://backend-node-$i.railway.internal:8080"
  done

  log "Configurando $count nós"
  for ((i=1; i<=count; i++)); do
    set_variable "cockroach-$i" PORT 8080
    set_variable "cockroach-$i" COCKROACH_ADVERTISE_ADDR "cockroach-$i.railway.internal:26257"
    set_variable "cockroach-$i" COCKROACH_JOIN "$join"

    set_variable "minio-$i" MINIO_ROOT_USER "$S3_ACCESS_KEY"
    set_variable "minio-$i" MINIO_ROOT_PASSWORD "$S3_SECRET_KEY"

    set_variable "backend-node-$i" PORT 8080
    set_variable "backend-node-$i" NODE_ID "backend-node-$i"
    set_variable "backend-node-$i" NODE_BOOTSTRAP true
    set_variable "backend-node-$i" DATABASE_URL "postgresql://root@cockroach-$i.railway.internal:26257/drive_clone?sslmode=disable"
    set_variable "backend-node-$i" BACKEND_ENDPOINT "http://backend-node-$i.railway.internal:8080"
    set_variable "backend-node-$i" S3_ENDPOINT "http://minio-$i.railway.internal:9000"
    set_variable "backend-node-$i" S3_BUCKET drive-clone
    set_variable "backend-node-$i" S3_ACCESS_KEY "$S3_ACCESS_KEY"
    set_variable "backend-node-$i" S3_SECRET_KEY "$S3_SECRET_KEY"
    set_variable "backend-node-$i" CONTROL_TOKEN "$CONTROL_TOKEN"
    set_variable "backend-node-$i" CONTROL_INTERVAL 2s
    set_variable "backend-node-$i" CONTROL_TIMEOUT 2s
    set_variable "backend-node-$i" MANAGER_LEASE_TTL 15s
    set_variable "backend-node-$i" FAILURE_THRESHOLD 3
    set_variable "backend-node-$i" SECURE_COOKIES true
    set_variable "backend-node-$i" ADMIN_LOGIN "$ADMIN_LOGIN"
    set_variable "backend-node-$i" ADMIN_PASSWORD "$ADMIN_PASSWORD"
    set_variable "backend-node-$i" FAULT_ACTUATOR_URL http://fault-actuator.railway.internal:8090
    set_variable "backend-node-$i" FAULT_ACTUATOR_TOKEN "$FAULT_ACTUATOR_TOKEN"

    backend_instance=$(wait_instance_id "backend-node-$i")
    sql_instance=$(wait_instance_id "cockroach-$i")
    storage_instance=$(wait_instance_id "minio-$i")
    targets=$(jq -c \
      --arg node "backend-node-$i" \
      --arg backend "$backend_instance" \
      --arg sql "$sql_instance" \
      --arg storage "$storage_instance" \
      '. + [
        {node_id:$node,component:"backend",railway_instance:$backend},
        {node_id:$node,component:"sql",railway_instance:$sql},
        {node_id:$node,component:"storage",railway_instance:$storage}
      ]' <<<"$targets")
  done

  set_variable load-balancer PORT 8080
  set_variable load-balancer CONTROL_TOKEN "$CONTROL_TOKEN"
  set_variable load-balancer CONTROL_ENDPOINTS "$endpoints"

  set_variable fault-actuator PORT 8090
  set_variable fault-actuator FAULT_ACTUATOR_MODE railway-ssh
  set_variable fault-actuator FAULT_ACTUATOR_TOKEN "$FAULT_ACTUATOR_TOKEN"
  set_variable fault-actuator FAULT_ACTUATOR_TARGETS "$targets"
  set_variable fault-actuator RAILWAY_SSH_HOST ssh.railway.com
  set_variable fault-actuator RAILWAY_SSH_PRIVATE_KEY "$RAILWAY_SSH_PRIVATE_KEY"
  set_variable fault-actuator RAILWAY_SSH_KNOWN_HOSTS "$RAILWAY_SSH_KNOWN_HOSTS"
  ok "variáveis configuradas"
}

deployment_id() {
  railway service status --service "$1" --json 2>/dev/null | jq -r '.deploymentId // empty'
}

wait_deployment() {
  local service=$1 old_id=$2 deadline=$((SECONDS + 1800)) current_id status
  while ((SECONDS < deadline)); do
    current_id=$(deployment_id "$service")
    status=$(railway service status --service "$service" --json 2>/dev/null | jq -r '.status // empty')
    if [[ -n $current_id && ($current_id != "$old_id" || -z $old_id) ]]; then
      case $status in
        SUCCESS) ok "$service implantado"; return ;;
        FAILED|CRASHED|REMOVED|SKIPPED) die "$service terminou com estado $status. Consulte: railway logs --service $service" ;;
      esac
    fi
    printf '.'
    sleep 5
  done
  die "tempo esgotado aguardando o deploy de $service"
}

deploy_service() {
  local service=$1 path=${2:-} old_id
  old_id=$(deployment_id "$service")
  log "Implantando $service"
  if [[ -n $path ]]; then
    railway up "$path" --path-as-root --service "$service" --detach --message "setup automatizado do Acervo" >/dev/null
  else
    railway up --service "$service" --detach --message "setup automatizado do Acervo" >/dev/null
  fi
  wait_deployment "$service" "$old_id"
}

initialize_database() {
  local attempt
  log "Inicializando o cluster CockroachDB"
  for attempt in {1..12}; do
    if railway ssh --service cockroach-1 -- /cockroach/cockroach sql --insecure --host=localhost:26257 --execute='SELECT 1' >/dev/null 2>&1; then
      break
    fi
    if railway ssh --service cockroach-1 -- /cockroach/cockroach init --insecure --host=localhost:26257 >/dev/null 2>&1; then
      break
    fi
    sleep 5
  done
  railway ssh --service cockroach-1 -- /cockroach/cockroach sql --insecure --host=localhost:26257 --execute='CREATE DATABASE IF NOT EXISTS drive_clone' >/dev/null
  ok "banco drive_clone disponível"
}

deploy_full_stack() {
  local count=$1 i
  for ((i=1; i<=count; i++)); do deploy_service "cockroach-$i"; done
  initialize_database
  for ((i=1; i<=count; i++)); do deploy_service "minio-$i"; done
  deploy_service fault-actuator backend
  for ((i=1; i<=count; i++)); do deploy_service "backend-node-$i"; done
  deploy_service load-balancer
}

public_domain() {
  local domains created domain
  domains=$(railway domain list --service load-balancer --json)
  domain=$(jq -r '.domains[0].domain // .domains[0].hostname // empty' <<<"$domains")
  if [[ -z $domain ]]; then
    created=$(railway domain --service load-balancer --port 8080 --json)
    domain=$(jq -r '.. | strings | select(test("railway\\.app$"))' <<<"$created" 2>/dev/null | head -n1)
    domain=${domain:-$(grep -Eo '[A-Za-z0-9.-]+\.railway\.app' <<<"$created" | head -n1)}
  fi
  [[ -n $domain ]] || die "deploy concluído, mas não foi possível obter o domínio público"
  printf 'https://%s' "$domain"
}

existing_node_count() {
  local numbers expected=1 number
  mapfile -t numbers < <(jq -r '.[].name | select(test("^backend-node-[0-9]+$")) | capture("backend-node-(?<n>[0-9]+)").n' <<<"$SERVICES_JSON" | sort -n)
  ((${#numbers[@]} > 0)) || die "o projeto não contém nenhum backend-node-N"
  for number in "${numbers[@]}"; do
    ((number == expected)) || die "numeração de nós não contígua: esperado backend-node-$expected"
    service_exists "cockroach-$number" || die "cockroach-$number ausente"
    service_exists "minio-$number" || die "minio-$number ausente"
    ((expected++))
  done
  printf '%s' "${#numbers[@]}"
}

observe_node_admission() {
  local node=$1 attempt output state
  log "Acompanhando a admissão de $node"
  for attempt in {1..60}; do
    output=$(railway ssh --service cockroach-1 -- /cockroach/cockroach sql --insecure --host=localhost:26257 --database=drive_clone --format=tsv --execute="SELECT node_id,state,synced_publication_generation FROM cluster_nodes WHERE node_id='$node'" 2>/dev/null || true)
    state=$(awk -F $'\t' -v node="$node" '$1 == node {print $2}' <<<"$output" | tail -n1)
    if [[ -n $state ]]; then
      printf '  %-24s %s\n' "$node" "$state"
      [[ $state == ready ]] && { ok "$node foi sincronizado e admitido"; return; }
    else
      printf '  aguardando registro de %s\n' "$node"
    fi
    sleep 3
  done
  warn "$node ainda não ficou pronto; acompanhe no painel administrativo e nos logs"
}

show_credentials_and_domain() {
  local domain=$1
  printf '\n\033[1;32mImplantação concluída\033[0m\n'
  printf 'Domínio: %s\n' "$domain"
  printf 'Usuário administrador: %s\n' "$ADMIN_LOGIN"
  printf 'Senha administrativa: %s\n' "$ADMIN_PASSWORD"
  printf 'Segredos locais: .railway-local/%s.env\n' "$RAILWAY_PROJECT_ID"
}
