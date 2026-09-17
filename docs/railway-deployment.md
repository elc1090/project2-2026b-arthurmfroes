# Implantação e falhas reais no Railway

## Roteiros interativos

O primeiro deploy pode ser feito sem configurar serviços pelo painel:

```bash
./scripts/setup-railway.sh
```

O roteiro autentica a CLI, permite criar um projeto ou escolher um existente,
pergunta a quantidade de nós, cria serviços e volumes, configura segredos e rede
privada, implanta a topologia, inicializa o CockroachDB, forma a replicação entre os
sites MinIO e cria o bucket versionado. O domínio público só é impresso depois da
eleição do gerenciador e da admissão de todos os nós.
Ele pode ser executado novamente depois de uma interrupção: serviços, volumes e
segredos existentes são reaproveitados.

Para demonstrar a entrada de um nó novo em um projeto já implantado:

```bash
./scripts/add-railway-node.sh
```

O segundo roteiro cria o próximo trio de serviços, atualiza os participantes do
CockroachDB, os alvos do atuador e as rotas conhecidas pelo load balancer. Ao final,
consulta o estado compartilhado até mostrar o nó sincronizado e `ready`.

Os únicos arquivos locais gerados ficam em `.railway-local/`, ignorado pelo Git e
com permissões restritas, e em `~/.ssh/acervo_railway_*`. A fingerprint apresentada
por `ssh.railway.com` exige confirmação no terminal.

## Topologia

A implantação completa usa serviços separados para que uma falha de componente não
derrube recursos do mesmo container:

| Papel | Quantidade | Dockerfile |
| --- | ---: | --- |
| Entrada Nginx | 1 | `nginx/Dockerfile` |
| Atuador de falhas | 1 | `backend/Dockerfile.actuator` |
| Backend | N | `railway/Dockerfile.backend` |
| CockroachDB | N | `railway/Dockerfile.cockroach` |
| MinIO | N | `railway/Dockerfile.minio` |

Os Dockerfiles Railway executam `tini` como PID 1. O workload fica num único grupo
filho e `/usr/local/bin/fault-signal` interrompe ou retoma esse grupo. O helper não
recebe PID, nome de processo ou comando do painel. O Compose local continua usando
`docker pause` e `docker unpause`; `./scripts/dev.sh` não depende desta configuração.

## Preparação do SSH

Crie uma chave Ed25519 exclusiva para o atuador e cadastre somente a pública na
conta Railway. A CLI é usada nessa preparação, fora do serviço implantado:

```bash
ssh-keygen -t ed25519 -f ~/.ssh/acervo_railway_fault -C acervo-railway-fault
railway ssh keys add --key ~/.ssh/acervo_railway_fault.pub --name acervo-fault-actuator
```

Os roteiros obtêm pela CLI o **Service Instance ID** de cada backend, CockroachDB e
MinIO. O Service ID e o Deployment Instance ID são identificadores diferentes e não
servem como usuário de `ssh.railway.com`. Para conferir os valores manualmente:

```bash
railway status --json | jq -r \
  '.environments.edges[].node.serviceInstances.edges[].node | [.serviceName,.id] | @tsv'
```

A Railway não publica uma lista autoritativa e estável das host keys do gateway.
Faça o bootstrap numa rede controlada, confira as fingerprints observadas e guarde
as linhas completas como segredo. O atuador usa `StrictHostKeyChecking=yes`; uma
chave nova bloqueia a operação até revisão e atualização explícita.

```bash
ssh-keyscan -t ed25519 ssh.railway.com > railway_known_hosts
ssh-keygen -lf railway_known_hosts
```

## Variáveis do atuador

Configure no serviço `fault-actuator`:

```text
FAULT_ACTUATOR_MODE=railway-ssh
FAULT_ACTUATOR_TOKEN=<segredo interno compartilhado somente com os backends>
RAILWAY_SSH_HOST=ssh.railway.com
RAILWAY_SSH_PRIVATE_KEY=<conteúdo multilinha da chave privada dedicada>
RAILWAY_SSH_KNOWN_HOSTS=<conteúdo multilinha de railway_known_hosts>
```

`FAULT_ACTUATOR_TARGETS` contém o mapeamento fechado. Repita as três entradas para
cada nó, trocando somente os IDs copiados do painel:

```json
[
  {"node_id":"backend-node-1","component":"backend","railway_instance":"<instance-backend-1>"},
  {"node_id":"backend-node-1","component":"sql","railway_instance":"<instance-cockroach-1>"},
  {"node_id":"backend-node-1","component":"storage","railway_instance":"<instance-minio-1>"}
]
```

Os valores opcionais `RAILWAY_SSH_CONNECT_TIMEOUT` e
`RAILWAY_SSH_COMMAND_TIMEOUT` aceitam durações Go e usam, respectivamente, `5s` e
`15s` por padrão. A imagem do atuador contém OpenSSH, mas não Railway CLI. Nenhum
token de API ou GraphQL é configurado no runtime.

Os backends recebem a URL privada do atuador e o mesmo token interno:

```text
FAULT_ACTUATOR_URL=http://fault-actuator.railway.internal:<porta>
FAULT_ACTUATOR_TOKEN=<mesmo segredo interno>
```

## Comportamento e verificação

Para `stop`, o atuador abre OpenSSH com argumentos fixos e executa
`/usr/local/bin/fault-signal stop`. Para `restore`, executa o mesmo helper com
`restore`. Se a conexão cair depois do sinal, abre outra sessão com `status`; só
declara sucesso após observar `stopped` ou `running`. Confirmação inconclusiva aparece
como `unknown`, sem antecipar uma decisão do cluster.

Antes de liberar os botões, verifique um componente por vez:

1. Execute `fault-signal status` por SSH e confirme `running`.
2. Solicite a parada e confirme indisponibilidade do serviço e estado `stopped`.
3. Abra uma segunda sessão, restaure e confirme `running`.
4. Observe separadamente a retirada, as rotas, a sincronização e a readmissão no
   painel; o atuador não altera membership nem lease.

Configuração incompleta, Instance ID desconhecido, topologia de processos diferente
da esperada ou host key nova falham fechados. Não use `StrictHostKeyChecking=no`,
`accept-new` ou fallback para `node_faults`.
