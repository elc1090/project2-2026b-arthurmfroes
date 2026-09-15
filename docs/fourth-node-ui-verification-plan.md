# Quarto nó pela interface — base nova

Plano offline; nenhum Docker, SQL ou HTTP executado nesta preparação. Reutiliza
`scripts/prepare-fourth-node-test.py` e `scripts/fourth-node-test.md`, substituindo
somente a etapa de registro via API pelo formulário real. A janela atual pertence
ao frontend para65MiB; provisionar apenas após nova liberação do coordenador.

## Preparação e orçamento

Projeto reservado: `acervo-node4-test`; rede externa: `acervo-infra-runtime_default`.
Na base nova não reutilizar dados da rodada antiga. O gerador recusa volumes já
existentes; se encontrá-los, parar e identificar a origem, sem removê-los. Usar
arquivo novo para não sobrescrever o JSON antigo:

```sh
python3 scripts/prepare-fourth-node-test.py \
  --output /tmp/acervo-fresh-node4-ui.compose.json
docker compose -f /tmp/acervo-fresh-node4-ui.compose.json config -q
```

O gerador inspeciona Docker, portanto estes comandos pertencem à janela liberada,
não à preparação offline. JSON0600 contém credenciais e fica fora do repositório.

Antes de iniciar, conferir portas27660/27904/28104 livres, volumes ausentes,
labels dos serviços de referência, três membros ready e identidade do clusterSQL.
Registrar image IDs, configuração e lista de recursos criados. O gerador fixa o
app na tag `acervo-application-backend:topology`: **não confiar nessa tag antiga**.
Comparar com a imagem efetiva do app-node-1 atual. Para não fazer build ou introduzir
código antigo, fixar no JSON gerado o image ID exato desse backend atual, após
revisão do coordenador. SQL e MinIO já são fixados por image ID de referência.

Limites já definidos pelo gerador: Cockroach512MiB/2CPUs/cache64MiB/SQL64MiB;
MinIO384MiB/0.5CPU/GOMEMLIMIT256MiB; backend192MiB/GOMEMLIMIT144MiB. Acréscimo máximo
de containers:1088MiB. Com1.9GiB disponíveis, a margem é pequena e não inclui
pressão de recuperação, browser e pagecache. Não fazer builds, teste grande ou
outro ensaio de disponibilidade em paralelo. Medir RAM/swap antes e durante;
se houver pressão/OOM, registrar e interromper, sem mudar silenciosamente saúde.

## Provisionar sem registrar

```sh
docker compose -f /tmp/acervo-fresh-node4-ui.compose.json up -d
```

Somente estes três serviços novos:

| Componente | Endereço interno | Porta local |
| --- | --- | --- |
| Cockroach4 | cockroach-4:26257 | 27660 |
| MinIO4 | minio-4:9000 | 27904 |
| Backend4 | app-node-4:8080 | 28104 |

Cockroach4 inicia com store vazio e `--join` dos três membros existentes; nunca
executar cockroach init. MinIO4 inicia vazio: não criar bucket nem adicionar site
manualmente. Backend4 usa NODE_BOOTSTRAP=false e banco local acervo_app_test.
Antes do formulário, deve estar fora da composição e readiness deve rejeitar
tráfego. Seu endpoint interno de identidade precisa responder autenticado com
clusterSQL correto, SQLnode distinto e deploymentID MinIO distinto. As leituras
não equivalem a registrar o nó.

Neste ponto passar a execução para o agente frontend, mantendo os serviços novos
ligados. Não enviar POST /api/admin/nodes pelo terminal ou outro cliente.

## Formulário que o frontend deve preencher

No painel “Associação e retirada”, clicar “Registrar nó provisionado”:

| Campo | Valor |
| --- | --- |
| Identidade do nó | app-node-4 |
| Endpoint do backend | http://app-node-4:8080 |
| Endpoint do banco | postgresql://cockroach-4:26257/acervo_app_test |
| Endpoint do armazenamento | http://minio-4:9000 |

O frontend define credential_profile=default e idempotency_key UUID. Não inserir
credenciais ou sslmode nos endpoints públicos. Clicar “Solicitar associação” e
capturar evidência da interação, requisição202, operação exibida e progressão.
Não confundir202 com nó admitido: exigir operação complete e quatro membros ready.
Se houver erro, preservar operação/chave/last_error e investigar antes de reenviar.

Usar arquivo já publicado antes da associação, pertencente ao usuário autenticado.
Registrar file_id/hash e verificar listagem e download direto do nó4 com essa
sessão, tamanho e SHA256. Admin não recebe acesso automático a arquivos de outros
owners; não usar arquivo da conta aleatória do harness com cookieadmin.
Confirmar recibo novo do nó4/geração atual. A UI comprova fluxo administrativo;
a leitura física sem peers é uma prova separada, se necessária.

## Encerramento

Não parar ou remover fisicamente nó4 enquanto ainda for membro. Retirar pelo fluxo
suportado do painel, ou pela API validada se coordenador optar: operação complete,
nóremoved, trêsready, SQL4decommissionado e MinIO4fora do peering. Só então:

```sh
docker compose -f /tmp/acervo-fresh-node4-ui.compose.json stop
```

Preservar volumes, JSON, logs, IDs e evidências. Se associação/retirada falhar,
manter recursos para diagnóstico; não usar down-v, reset de store ou nova chave
para contornar estado pendente. Depois da retirada, esse mesmo volume SQL não é
um nó vazio reutilizável para outra associação.
