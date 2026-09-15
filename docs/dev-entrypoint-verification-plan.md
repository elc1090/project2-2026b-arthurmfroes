# Verificação real de scripts/dev.sh

Objetivo de8.6: iniciar o Compose do repositório pelo script fornecido, validar
bootstrap, login compartilhado e transferência pelos três backends. O plano abaixo foi executado em 15/09/2026. Os resultados e limites da prova
estão registrados ao final.

## Isolamento e restrições encontradas

Usar `COMPOSE_PROJECT_NAME=acervo-dev-entrypoint-proof` em todos os comandos desta
prova. Confirmar antes que esse projeto não existe; não reutilizar recursos de
outro ensaio. O namespace separa rede/volumes/containers, mas não portas publicadas.
Portas fixas do Compose:8080;26257–26259;8088–8090;9000–9001;9010–9011;9020–9021.
Checar listeners e bindings Docker antes; conflito interrompe o roteiro, não
encerra processos desconhecidos. Essas portas escutam todas as interfaces no
Compose atual: registrar isso como contrato observado do ambiente dev.

`scripts/dev.sh` fixa `--file docker-compose.dev.yml`. Os argumentos adicionais
vão para `up`, não para opções globais do Compose. Portanto COMPOSE_FILE não é um
mecanismo de override desse script. Não editar nem substituir o script pelo comando
`docker compose up` durante a prova.

Antes de iniciar: salvar relatórios, journals, image IDs e estado dos serviços dos
projetos conhecidos `acervo-app-test` e `acervo-infra-runtime`. Parar esses projetos
somente após liberação do coordenador, preservando volumes e deixando registrada
a lista de containers que estavam rodando. Não tocar projetos desconhecidos.
Restaurar essa lista ao final, infraestrutura antes da aplicação.

## Recursos e inicialização

O Compose dev não limita RAM/CPU e o start.sh do Cockroach não limita cache/SQL.
Isso difere do ensaio anterior com2CPUs/512MiB por Cockroach. Não atribuir aqueles
limites ao caminho dev. Mesmo parando a stack anterior, confirmar memória livre,
swap e disco antes do bootstrap.

Execução direta, se houver orçamento para os defaults:

```sh
COMPOSE_PROJECT_NAME=acervo-dev-entrypoint-proof ./scripts/dev.sh -d
```

Alternativa para host apertado, sujeita à revisão do coordenador: criar pelo próprio
script sem iniciar, ajustar somente os containers desse namespace e então iniciar
sem recriação. Não usar esta variante sem confirmar o suporte das flags no Compose
instalado e inspecionar os limites efetivos antes do segundo comando.

```sh
COMPOSE_PROJECT_NAME=acervo-dev-entrypoint-proof ./scripts/dev.sh --no-start
# docker update com IDs/labels validados, limites aprovados pelo coordenador.
COMPOSE_PROJECT_NAME=acervo-dev-entrypoint-proof ./scripts/dev.sh -d --no-recreate
```

Nesta alternativa o relatório precisa dizer “script real, com limites externos no
harness”; não prova que o Compose distribui recursos sozinho. Limitar memória do
container também não equivale a configurar explicitamente cache/SQL; conferir os
valores efetivos do Cockroach e abortar em OOM ou pressão relevante. Não ajustar
silenciosamente timeouts de saúde para obter sucesso.

Bootstrap esperado: Cockroach-init termina0 após criar drive_clone; MinIO-init
termina0 após confirmar os três peers e o bucket; três backends concluem migrações
próprias e ficam ready; LB serve a interface Acervo e passa a rotear. Capturar logs
dos inicializadores, exit codes e image IDs/digests efetivamente utilizados. Tags
latest do MinIO/mc tornam o digest uma parte necessária do registro.

## Prova funcional pequena

O Compose não publica portas dos backends. Usar Python já instalado no container
load-balancer (`docker exec -i ... python3 -`) como cliente na rede do projeto;
não publicar portas adicionais só para testar. Destinos internos:
`http://backend-node-1:8080`, `http://backend-node-2:8080`,
`http://backend-node-3:8080`. O frontend pode ser conferido em localhost:8080.

1. Esperar GET /health/ready200 nos três destinos. Limite explícito de180s para
   bootstrap e registrar tentativas; se exceder, guardar logs e declarar falha.
2. POST /api/login com admin/admin no nó1, reter Set-Cookie apenas na memória do
   cliente. GET /api/me no nó1/2/3 com a mesma cookie deve retornar o mesmo usuário.
   Login novo individual nos nós2/3 também deve funcionar. Não imprimir cookies.
3. Criar pela API uma pasta com nome único e upload pequeno com três partes,
   tamanho e SHA256 conhecidos. POST /api/uploads no nó1; enviar parte0 no nó1,
   parte1 no nó2 e parte2 no nó3; todos PUT retornam204. Usar idempotency_key UUID.
4. Consultar até available (prazo90s), guardar operation_id/file_id e metadata.
   Download pelo LB e diretamente pelos três nós precisa devolver o mesmo tamanho
   e SHA256. Conferir pasta e arquivo pela API nos três nós. Erro/timeout não é
   sucesso; não substituir a checagem por acesso direto ao MinIO.
5. Parar e iniciar novamente pelo script no mesmo namespace, sem remover volumes.
   Após readiness, repetir login, listagem e download para demonstrar persistência.
6. Guardar relatório, logs e versão dos scripts. Parar o projeto de prova e restaurar
   exclusivamente os serviços anteriores registrados. Não executar down-v por padrão.

O teste de bootstrap não substitui go test, frontend validation, browser retomada
ou teste de2GiB. São evidências separadas de8.6/8.5.

## Orçamento do arquivo de referência de2GiB

Não executar esse teste como parte do bootstrap acima. A linha de base deve usar
arquivos pequenos; programar2GiB em janela própria, depois de medir espaço real.

Estimativa conservadora antes de verificar deduplicação/replicação física:

- Fonte local para o navegador:2GiB.
- Partes temporárias replicadas nos três sites: até6GiB.
- Três escritas explícitas finais com versões distintas, cada versão replicada
  para três sites: até18GiB lógicos de objetos finais.
- Download salvo pelo navegador: mais2GiB se mantido, além de SQL, imagens, logs,
  metadados e margem livre do MinIO.

Isso chega a28GiB antes da margem, e retries/recuperações podem acrescentar versões.
Não confundir deduplicação observada no filesystem com capacidade lógica garantida.
Medir alocação física por volume, versões existentes e requisitos de espaço livre
antes de concluir viabilidade. Evitar duplicar o download local quando a prova
permite SHA256 em fluxo; o navegador ainda precisa de fonte persistente de2GiB.

Se faltar capacidade, declarar8.5 pendente ou reservar disco dedicado. Candidatos
à limpeza: somente arquivos-fonte/downloads gerados por nossos ensaios, logs
redundantes já preservados e recursos exatos de projetos de prova cujo descarte
foi autorizado. Enumerar paths/IDs/tamanho e preservar relatórios, checksums,
journals e evidências antes. Não apagar versões S3 referenciadas nem volumes das
stacks em uso para abrir espaço; não usar docker system prune ou varredura ampla
por prefixo. Remoção de volumes exige decisão explícita do coordenador/usuário.

## Defaults confirmados na imagem efetiva

A consulta `cockroach start --help` da versão 23.2 confirmou cache padrão de
128 MiB, corrigindo a hipótese anterior de cache proporcional ao host. O orçamento
SQL padrão é 25% da memória disponível; no container limitado a 512 MiB, o help
mostrou 128 MiB. O orçamento SQL é um limite, não memória toda reservada na partida.
A prova do script com dados pequenos deve primeiro avaliar os defaults reais,
sem introduzir limites externos apenas com base na estimativa anterior.
Evidência: `/tmp/acervo-cockroach23-start-help.txt`.

## Resultado executado em 15/09/2026

`COMPOSE_PROJECT_NAME=acervo-dev-entrypoint-proof ./scripts/dev.sh -d` executou
duas vezes com sucesso, antes e depois de `docker compose stop`. Os dois
inicializadores terminaram com código 0. Os containers usaram os defaults do
Compose, sem limites externos de CPU/RAM e sem OOM registrado. O namespace isolou
volumes e rede; as portas publicadas foram as originais.

O cliente `scripts/verify-dev-client.py` executou dentro do load-balancer:

```sh
docker compose -p acervo-dev-entrypoint-proof -f docker-compose.dev.yml exec -T load-balancer python3 - --execute < scripts/verify-dev-client.py
# Após stop e nova execução do script, preservando os volumes:
docker compose -p acervo-dev-entrypoint-proof -f docker-compose.dev.yml exec -T load-balancer python3 - --resume < scripts/verify-dev-client.py
```

A primeira fase registrou 49 requisições e a segunda 31. Houve respectivamente
23 e 12 respostas 503, todas em `/health/ready` durante o bootstrap. As operações
funcionais responderam 200, 201 ou 204. Login no nó 1 produziu uma sessão aceita
pelos três nós, com a mesma identidade. O roteiro não repetiu logins independentes
nos nós 2 e 3; essa variação do plano não foi executada nesta passagem.

Três partes foram enviadas a backends distintos. Após publicação, o LB e cada
backend devolveram o arquivo e listaram sua pasta e seu ID. Após reinício, a mesma
verificação passou para o mesmo arquivo, sem novo upload:

- Operação `c2fe5589-fceb-4e99-95ac-faf646734cbc`.
- Arquivo `7d37c7b9-513c-4808-b1c7-1bafb6fde972`, 147 bytes.
- SHA-256 `b18322872c115a7ce066679944ac3669e34d37f7e1ccd1dba1b3249189df59bc`.

Os relatórios `/tmp/acervo-dev-client-report.json` e
`/tmp/acervo-dev-client-resume-report.json` registram `complete=true`.
O estado exportado contém IDs e checksum, sem credenciais. Logs de build,
reinício e serviços estão em `/tmp/acervo-dev-entrypoint-*.log`. O inventário
`/tmp/acervo-dev-entrypoint-images.json` registra imagens e limites efetivos.
MinIO usou `69b2ec208575`, mc `c2ad77420d33` e Cockroach-init `69d4b04f16d9`.
O build da entrada gerou `4c286dbb35aa`.

Ao terminar, o projeto da prova foi parado e seus volumes preservados. Os dez
containers que estavam ativos antes foram restaurados; os três backends voltaram
a `ready`. O quarto nó retirado permaneceu parado. A prova funcional pequena
não mede memória nem substitui o teste de 2 GiB.
