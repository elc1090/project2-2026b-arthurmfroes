# Verificação da infraestrutura de desenvolvimento

Em 2026-09-15, as verificações reais das tarefas 1.3–1.5 passaram no projeto Docker
isolado `acervo-infra-runtime`. O bloqueio anterior do socket foi resolvido nesta
sessão. Nenhum volume do ambiente de desenvolvimento foi usado ou apagado.

O teste confirma infraestrutura, persistência local e comportamento de bootstrap.
Não confirma admissão pelo gerenciador, upload da aplicação ou transferência de
2 GiB pelo navegador. Os backends testados contêm a base da tarefa 1.1 e ainda
respondem 503 em `/health/ready` por não implementarem admissão.

## Imagens e recursos efetivamente usados

Docker Engine 28.5.1, Linux amd64. O Docker Hub rejeitou o pull de `minio/mc:latest`
com `pull access denied`. O Compose passou a usar o registro oficial Quay para mc
e servidor MinIO; ambos foram baixados e executados com sucesso.

| Imagem | Versão observada | Digest |
| --- | --- | --- |
| cockroachdb/cockroach:v23.2.0 | v23.2.0 | `sha256:d4cae01f1db1b4056c75ada36cb6c3e4994fc84e38cf28930c4a9c8b191c9085` |
| quay.io/minio/minio:latest | RELEASE.2025-09-07T16-13-09Z | `sha256:14cea493d9a34af32f524e538b8346cf79f3321eff8e708c1e2960462bd8936e` |
| quay.io/minio/mc:latest | RELEASE.2025-08-13T08-35-41Z | `sha256:a7fe349ef4bd8521fb8497f55c6042871b2ae640607cf99d9bede5e9bdf11727` |

A prova vale para essas imagens. As tags `latest` permanecem mutáveis; uma mudança
de digest exige repetir a prova antes de presumir o mesmo contrato de armazenamento.

O override de teste limita cada CockroachDB a 512 MiB, cache de 64 MiB e memória SQL
de 64 MiB. Cada MinIO recebe limite de 384 MiB e `GOMEMLIMIT=256MiB`. Cada backend
base recebe 64 MiB. Nenhum dos seis serviços de dados registrou OOMKilled.
A configuração usa localhost 27657–27659 para SQL, 27901–27903 para S3 e
27801–27803 para os backends. Não publica as portas do desenvolvimento normal.

## Resultados de bootstrap e reinício

- Compose expandido confirmou três pares de endpoints locais e seis volumes distintos.
- Partida fria terminou com `cockroach-init` e `minio-init` em `Exited (0)`.
- Os três gateways SQL leram `id=1` da tabela `drive_clone.infrastructure_probe`.
- Reexecução dos inicializadores terminou com código 0, preservando os volumes e peers.
- Reinício dos três bancos preservou o marcador e a reexecução SQL terminou com código 0.
- MinIO foi reiniciado individualmente com seus dois peers parados durante a prova
  de persistência. Os objetos, versões e multipart aberto sobreviveram.
- Com backend, CockroachDB e MinIO do nó 1 parados, reiniciaram-se os componentes dos
  nós 2 e 3. O bootstrap SQL encontrou `cockroach-2:26257`, terminou com código 0 e
  ambos os gateways leram o marcador preservado.
- Nessa partida degradada, `minio-init` terminou com código 1 e erro explícito ao
  alcançar `minio-1`. Os backends 2 e 3 responderam `live=200`, `ready=503` mesmo
  assim. Não houve dependência global impedindo a partida dos sobreviventes.
- Após restaurar o nó 1, ambos os inicializadores voltaram a terminar com código 0.

O backend não deve interpretar processo vivo como elegibilidade. A verificação de
admissão real e atendimento aos usuários nos sobreviventes pertence às etapas de
controle e integração da aplicação.

O bootstrap SQL procura um gateway disponível antes de tentar `init`. Falha de
inicialização não apaga dados nem cria um cluster alternativo. O script MinIO só
executa `replicate add` quando os três sites informam replicação desabilitada.
Peering incompleto, duplicado ou inesperado encerra com erro para inspeção, sem
remover sites. O Compose mantém `restart: on-failure` nos inicializadores.

## Prova de persistência local

`scripts/prove-minio-local.py` terminou com código 0. Para cada site, com os dois
peers parados, o roteiro leu a versão previamente replicada, gravou explicitamente
os mesmos bytes em uma nova versão, completou multipart de 7 MiB e gravou objeto
vazio. Depois reiniciou o site ainda isolado e conferiu tamanho e SHA-256 de todas
as versões novamente. Uma parte de multipart incompleto também sobreviveu ao
reinício local; a sessão temporária foi abortada após a verificação.

A chave base foi `infrastructure-proof/feb96f53-0982-42db-902a-9ff4a568d1be/already-replicated`.
A referência exata ao prefixo e os valores observados são registrados abaixo.

| Site | Nova versão explícita da chave replicada | Versão multipart de 7 MiB |
| --- | --- | --- |
| 1 | `aaab3ad8-889a-45ff-9538-63b78a1ccb0c` | `956b72ae-c599-40ef-a577-3e3c181eb8e0` |
| 2 | `a0b3787a-b0bf-4295-8f7b-456c1e204ce2` | `423e24ee-0598-4952-b05b-26f022b50963` |
| 3 | `0ececabb-713d-49fa-87a6-985c67aaf8c2` | `17843941-372f-4274-a293-dd7fa3327915` |

A versão original `e13d6651-39bc-4663-9b4f-4acbd98aef29` permaneceu legível nos três
sites. SHA-256 dos 49 bytes originais e regravados:
`c72f9062a785718360e5294dc8727bb103500fee5c44330cea2005b9f15eab10`.
SHA-256 dos 7.340.032 bytes multipart:
`3e79377e1ee3cabdaba0f1e47f0597f83f1cbdab28549563bdc58b9adf5fc1f7`.
O objeto vazio retornou o SHA-256 padrão do conteúdo vazio. ETag multipart continha
sufixo de contagem de partes e não foi usado como checksum do arquivo.

Esse resultado permite implementar recibos após escrita explícita local, ligados à
versão física, tamanho, SHA-256 e geração do storage. Não presume que uma sessão
multipart aberta seja replicada para outro site. Não comprova fsync sob corte físico
de energia nem autoriza apagar versões referenciadas.

## Prova de HEAD e GET por proxy

`scripts/prove-minio-proxy.py` terminou com código 0. O roteiro criou o bucket de
teste `proxy-proof-5a6adf424ebd` e desabilitou temporariamente somente suas regras
outbound no site 1. O objeto `source-only`, versão
`78ad245f-59ca-40be-a3f8-7dc101448785`, existia apenas na origem.

| Observação | Site 2 | Site 3 |
| --- | --- | --- |
| ListObjectVersions antes e depois do GET | vazio | vazio |
| HEAD com origem disponível | 200 | 200 |
| GET com origem disponível | bytes corretos | bytes corretos |
| HEAD com origem parada | timeout de 15 s | timeout de 15 s |
| HeadBucket local com origem parada | 200 | 200 |

As regras originais foram restauradas e a origem voltou a executar ao finalizar.
Isso demonstra que HEAD ou GET bem-sucedido pode vir de outro site. Esses resultados
não podem gerar recibos de persistência local. O timeout é registrado como timeout,
não como resposta HTTP 404 ou prova de perda definitiva dos dados.

## Reproduzir em ambiente isolado

Os scripts só aceitam o nome de projeto `acervo-infra-runtime` e usam as portas de
localhost acima. Reservar essas portas e coordenar os reinícios com qualquer outro
agente que esteja usando os bancos. Os seis volumes de teste são preservados.

```sh
python3 scripts/prepare-infrastructure-test.py > /tmp/acervo-infra-runtime.json
acervo_check() {
  docker compose --file /tmp/acervo-infra-runtime.json "$@"
}
acervo_check config --quiet
acervo_check up -d cockroach-1 cockroach-2 cockroach-3 minio-1 minio-2 minio-3 cockroach-init minio-init
acervo_check logs cockroach-init minio-init
acervo_check ps --all
acervo_check build backend-node-1
acervo_check up -d --no-build backend-node-1 backend-node-2 backend-node-3
```

Aguardar os dois inicializadores terminarem com código 0. Os backends da tarefa
1.1 retornam liveness 200 e readiness 503. Um inicializador em reinício não é sucesso.
O preparador substitui apenas no teste o wrapper Cockroach pelos argumentos
equivalentes mais os limites de memória; o Dockerfile Cockroach não foi reconstruído
nessa execução. O Dockerfile backend foi construído e sua imagem executada.

Python e boto3 são ferramentas do roteiro, sem dependência na aplicação:

```sh
uv venv /tmp/acervo-s3-proof-venv
uv pip install --python /tmp/acervo-s3-proof-venv/bin/python boto3==1.43.94
/tmp/acervo-s3-proof-venv/bin/python scripts/prove-minio-local.py --compose /tmp/acervo-infra-runtime.json --report /tmp/acervo-minio-local-proof.json
/tmp/acervo-s3-proof-venv/bin/python scripts/prove-minio-proxy.py --compose /tmp/acervo-infra-runtime.json --report /tmp/acervo-minio-proxy-proof.json
```

Ambos os scripts param e reiniciam apenas sites MinIO do projeto de teste, restauram
os serviços ao terminar e produzem JSON com as versões, checksums e resultados.
Arquivos de prova permanecem nos volumes, com prefixos/buckets exclusivos por execução.

Para repetir a verificação SQL e a partida degradada:

```sh
acervo_check exec -T cockroach-1 cockroach sql --insecure --host=localhost:26257 --database=drive_clone --execute="CREATE TABLE IF NOT EXISTS infrastructure_probe (id INT PRIMARY KEY); INSERT INTO infrastructure_probe VALUES (1) ON CONFLICT DO NOTHING"
for n in 1 2 3; do
  acervo_check exec -T "cockroach-$n" cockroach sql --insecure --host=localhost:26257 --database=drive_clone --execute="SELECT * FROM infrastructure_probe"
done
acervo_check run --rm --no-deps cockroach-init
acervo_check run --rm --no-deps minio-init
acervo_check restart cockroach-1 cockroach-2 cockroach-3
# Após os bancos voltarem, repetir as leituras e o inicializador SQL.
acervo_check stop cockroach-1 minio-1 backend-node-1
acervo_check restart cockroach-2 cockroach-3 minio-2 minio-3 backend-node-2 backend-node-3
acervo_check run --rm --no-deps cockroach-init
acervo_check run --rm --no-deps minio-init
# O último comando deve falhar; conferir SQL e liveness dos sobreviventes.
acervo_check start cockroach-1 minio-1 backend-node-1
```

Depois da restauração, repetir ambos os inicializadores. Não usar `down -v`.

## Verificações locais e fontes

`python3 scripts/verify-infrastructure.py`, `sh -n cockroach/init-dev.sh
minio/init-dev.sh` e `git diff --check` passaram. O primeiro verifica Compose e nove
cenários MinIO mais quatro SQL com CLIs simulados. Esses testes complementam as
provas reais descritas acima.

O parser acompanha o código oficial de
[mc admin replicate info](https://github.com/minio/mc/blob/master/cmd/admin-replicate-info.go).
A tabela efetiva do mc foi validada pelas partidas reais. Usa somente builtins do
shell porque o [Dockerfile do mc](https://github.com/minio/mc/blob/master/Dockerfile.release)
não instala jq/awk. Formato desconhecido deve falhar até revisão.
O registro Quay consta do [build oficial do mc](https://github.com/minio/mc/blob/master/docker-buildx.sh)
e do [Compose oficial MinIO](https://github.com/minio/minio/blob/master/docs/orchestration/docker-compose/docker-compose.yaml).
As URLs SQL seguem os
[parâmetros CockroachDB](https://www.cockroachlabs.com/docs/v23.2/connection-parameters).


## Partições reais da aplicação em 15/09/2026

A imagem backend `f18a8b88` foi executada no projeto isolado `acervo-app-test`, com
três Cockroach23.2 a 1CPU/512MiB e três MinIO independentes. O controle usou intervalo
1s, sondagem2s, lease12s e duas falhas consecutivas. A fase `network` de
`scripts/verify-distributed-failures.py` passou em 137.8s.

| Condição | Resultado observado |
| --- | --- |
| Rede do gerenciador app-node-3 desconectada | app-node-2 assumiu em18.406s; mandato26→27; sobreviventes publicaram e baixaram bytes com SHA igual |
| Rede do Cockroach1 desconectada | backend1 excluído em11.556s e resposta direta503; nós2/3 publicaram e baixaram bytes iguais |
| Cockroach2/3 pausados, sem quorum | GET e PUT em cada backend retornaram503; após retorno operação seguiu pending, sem parte confirmada ou arquivo publicado |

As quatro ações foram restauradas, preservando volumes, e o preflight final confirmou
três nós prontos. Não houve eleição forçada nem cancelamento manual de sessão SQL.
Os arquivos de execução são `/tmp/acervo-network-v2-report.json`,
`/tmp/acervo-network-v2-journal.json` e `/tmp/acervo-network-v2.log`.

A rodada anterior expôs uma transação ociosa que mantinha o lock de configuração por
mais de dois minutos após uma partição. As conexões da aplicação agora impõem no
servidor prazos de5s para inatividade transacional e statements, e10s para transações.
Migrações ampliam os dois últimos para30s dentro da própria transação. A regressão
SQL observou liberação automática do lock em5.100s e rejeição do commit antigo.

Os 15 casos de simulação por nó/componente e stop de cada backend também passaram
no conjunto de três execuções anteriores. Cada simulação comprovou503 em conexão
HTTP persistente, seguida de upload/download pelos sobreviventes. Essas execuções
não são registradas como uma única suíte completa. A prova complementar de
[perda de autoridade](authority-loss-verification.md) passou posteriormente: quatro
novas requisições503 e dois downloads já iniciados encerrados incompletos.
A [conferência física](../frontend/verification/memory-256-result.md) também confirmou
o arquivo de256MiB nas versões dos três sites com seus peers desligados.

## Suíte completa após recriação dos dados de teste

Após autorização do usuário para apagar os nove volumes de teste, a infraestrutura
foi recriada. `verify-distributed-failures.py --execute --phase all` passou em
194,52 segundos. Foram 15 simulações, três paradas de backend e duas partições
de rede. Os 20 casos publicaram e baixaram o arquivo nos dois sobreviventes; as
138 requisições dessas transferências retornaram 200, 201 ou 204. As 15 simulações
receberam 503 no mesmo socket persistente do backend excluído.

A exclusão foi observada entre 1,421 e 13,405 segundos, dentro da espera máxima
de 90 segundos do roteiro. Esse tempo inclui aplicar a falha e consultar o estado;
não representa somente o intervalo das sondagens. Na perda de quorum, seis
requisições retornaram 503. A operação continuou pendente, sem parte ou publicação,
e foi cancelada após a recuperação. As 22 ações do journal foram restauradas e
o preflight final confirmou três nós prontos.

Relatório `/tmp/acervo-fresh-failures-report.json`, `complete: true`; journal e log
com o mesmo prefixo. A fase `all` não exige sucessão dinâmica do gerenciador; essa
condição foi comprovada separadamente pela fase `network` anterior.
