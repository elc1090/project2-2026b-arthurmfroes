# Teste isolado de associação e retirada do quarto nó

Este roteiro usa a rede externa `acervo-infra-runtime_default` e o projeto separado `acervo-node4-test`. O gerador apenas inspeciona Docker e escreve JSON. Os comandos de inicialização e API abaixo ficam para a janela de testes liberada pelo coordenador.

O backend deve estar disponível como `acervo-application-backend:topology`, com migração 003, rotas administrativas e CLI Cockroach 23.2. Os três backends existentes também precisam dessa implementação. O roteiro não monta Docker socket em nenhum backend.

## Preparar sem iniciar serviços

```sh
python3 scripts/prepare-fourth-node-test.py --output /tmp/acervo-node4-test.compose.json
docker compose -f /tmp/acervo-node4-test.compose.json config -q
```

O JSON contém credenciais do ambiente acadêmico e nasce com permissão 0600, fora do repositório. O script recusa sobrescrever arquivo ou reutilizar volumes já existentes. Para retomar um teste, use seu JSON existente e preserve os volumes.

O Cockroach usa a imagem exata do membro 1, conferido como `v23.2.0`, com 512 MiB e 2 CPUs. O MinIO usa o mesmo image ID do site 1, com 384 MiB e 0,5 CPU. O app usa `NODE_ID=app-node-4`, `NODE_BOOTSTRAP=false`, login acadêmico `admin/admin`, simulação de falhas habilitada e o token de controle do app de referência. Copia também os intervalos de controle já testados.

| Serviço | Endereço na rede de teste | Porta local |
| --- | --- | --- |
| Cockroach | `cockroach-4:26257` | 27660 |
| MinIO | `minio-4:9000` | 27904 |
| Backend | `app-node-4:8080` | 28104 |

Não há inicializador de cluster SQL, bucket ou replicação neste Compose. O Cockroach associa armazenamento novo via `--join` aos três membros existentes. O MinIO precisa permanecer sem buckets até a associação administrativa.

## Executar após liberação

Confirme antes que não existe outro teste alterando disponibilidade ou topologia. Publique um arquivo de exemplo pelo app atual e guarde seu ID e arquivo original para comparar o download depois. É necessário um arquivo anterior à associação para provar a sincronização do quarto nó.

```sh
docker compose -f /tmp/acervo-node4-test.compose.json up -d
```

O backend novo deve permanecer fora do tráfego até registro e admissão. Aguarde o endpoint interno de identidade estar acessível; não crie bucket para contornar uma espera. O painel consulta esse endpoint autenticado ao registrar o nó.

Os exemplos seguintes usam `curl`, `jq` e o balanceador de testes observado em `127.0.0.1:28100`:

```sh
umask 077
ACERVO_TEST_API=http://127.0.0.1:28100
ACERVO_TEST_COOKIES=/tmp/acervo-node4-test.cookies
curl --fail-with-body -sS -c "$ACERVO_TEST_COOKIES" \
  -H "Origin: $ACERVO_TEST_API" -H 'Content-Type: application/json' \
  -d '{"login":"admin","password":"admin"}' "$ACERVO_TEST_API/api/login" \
  -o /tmp/acervo-node4-login.json

curl --fail-with-body -sS -b "$ACERVO_TEST_COOKIES" \
  -H "Origin: $ACERVO_TEST_API" -H 'Content-Type: application/json' \
  -d '{"idempotency_key":"node4-join-001","node_id":"app-node-4","backend_endpoint":"http://app-node-4:8080","database_endpoint":"postgresql://cockroach-4:26257/acervo_app_test","storage_endpoint":"http://minio-4:9000","credential_profile":"default"}' \
  "$ACERVO_TEST_API/api/admin/nodes" -o /tmp/acervo-node4-join.json
jq '{id,node_id,kind,stage,last_error}' /tmp/acervo-node4-join.json
ACERVO_JOIN_ID=$(jq -r .id /tmp/acervo-node4-join.json)
ACERVO_NODE4_ID=$(jq -r .node_id /tmp/acervo-node4-join.json)
```

Exigir HTTP 202 e guardar o identificador da operação. Repetir a mesma requisição deve devolver a mesma operação. Os endpoints de registro não contêm credenciais ou parâmetros de conexão; o `sslmode` pertence somente ao ambiente privado do backend. O perfil `default` deve resolver para credenciais administrativas do teste e gateway SQL do executor sobrevivente.

Consultar até `stage=complete`, sem tratar uma resposta 202 como conclusão:

```sh
curl --fail-with-body -sS -b "$ACERVO_TEST_COOKIES" \
  "$ACERVO_TEST_API/api/admin/node-operations/$ACERVO_JOIN_ID" | jq .
curl --fail-with-body -sS -b "$ACERVO_TEST_COOKIES" \
  "$ACERVO_TEST_API/api/admin/cluster" -o /tmp/acervo-node4-cluster.json
jq '[.nodes[] | {node_id,state,reason}]' /tmp/acervo-node4-cluster.json
jq -e '[.nodes[] | select(.state=="ready")] | length==4' /tmp/acervo-node4-cluster.json
```

A operação completa a associação de infraestrutura; os quatro estados `ready` só aparecem depois da sincronização e barreira de admissão. Em timeout, consultar `last_error` e preservar o ambiente. Não repetir com uma chave nova.

## Provar os bytes no quarto backend

Preencha com o ID e caminho do arquivo publicado antes da associação. A requisição deve ir diretamente ao nó 4:

```sh
ACERVO_FILE_ID='ID_DO_ARQUIVO_PUBLICADO'
ACERVO_SOURCE_FILE='/caminho/do/arquivo-original'
curl --fail-with-body -sS -b "$ACERVO_TEST_COOKIES" \
  "http://127.0.0.1:28104/api/files/$ACERVO_FILE_ID/download" \
  -o /tmp/acervo-node4-download.bin
cmp "$ACERVO_SOURCE_FILE" /tmp/acervo-node4-download.bin
```

Exigir HTTP 200 e `cmp` com exit code 0. Não usar somente contagem de recibos para declarar que os bytes foram recuperados.

## Retirar o quarto e conservar três

```sh
curl --fail-with-body -sS -b "$ACERVO_TEST_COOKIES" \
  -H "Origin: $ACERVO_TEST_API" -H 'Content-Type: application/json' \
  -d '{"idempotency_key":"node4-retire-001"}' \
  "$ACERVO_TEST_API/api/admin/nodes/$ACERVO_NODE4_ID/retire" \
  -o /tmp/acervo-node4-retire.json
ACERVO_RETIRE_ID=$(jq -r .id /tmp/acervo-node4-retire.json)
curl --fail-with-body -sS -b "$ACERVO_TEST_COOKIES" \
  "$ACERVO_TEST_API/api/admin/node-operations/$ACERVO_RETIRE_ID" | jq .
curl --fail-with-body -sS -b "$ACERVO_TEST_COOKIES" \
  "$ACERVO_TEST_API/api/admin/cluster" -o /tmp/acervo-node4-after.json
jq -e '[.nodes[] | select(.state=="ready")] | length==3' /tmp/acervo-node4-after.json
jq -e --arg id "$ACERVO_NODE4_ID" '.nodes[] | select(.id==$id) | .state=="removed"' /tmp/acervo-node4-after.json
```

Exigir operação `complete`, nó 4 `removed`, três nós `ready`, Cockroach 4 decommissionado e ausência do site 4 no peering dos três sobreviventes. Se o quarto era gestor, a continuidade deve ocorrer por outro gestor após expirar sua lease. A aplicação não apaga volumes nem desliga processos.

## Comprovar que 3 → 2 é recusado sem prender a fila

Com os três sobreviventes estáveis, escolher um deles e solicitar retirada. A API executa o preflight de leitura antes de persistir nova intenção. Nesta configuração com três réplicas obrigatórias, deve responder 409 `cockroach_topology_blocked`.

```sh
ACERVO_SURVIVOR_ID=$(jq -r '.nodes[] | select(.node_id=="app-node-3") | .id' /tmp/acervo-node4-after.json)
curl -sS -b "$ACERVO_TEST_COOKIES" -H "Origin: $ACERVO_TEST_API" \
  -H 'Content-Type: application/json' -d '{"idempotency_key":"node3-rejected-001"}' \
  -w '\nHTTP %{http_code}\n' \
  "$ACERVO_TEST_API/api/admin/nodes/$ACERVO_SURVIVOR_ID/retire"
curl --fail-with-body -sS -b "$ACERVO_TEST_COOKIES" \
  "$ACERVO_TEST_API/api/admin/node-operations" -o /tmp/acervo-node4-operations.json
jq -e '[.operations[] | select(.idempotency_key=="node3-rejected-001")] | length==0' \
  /tmp/acervo-node4-operations.json
```

Conferir novamente três `ready`. Esse resultado depende da política de réplicas; uma resposta 202 inesperada é falha do teste e exige investigação, não esperar a retirada concluir.

Ao terminar a retirada confirmada, `docker compose -f /tmp/acervo-node4-test.compose.json stop` para os três processos do projeto isolado. Não usar `down -v`, apagar volumes, executar `cockroach init` ou reiniciar o quarto como se seu armazenamento decommissionado fosse novo. Guardar JSON, respostas e volumes como evidência.
