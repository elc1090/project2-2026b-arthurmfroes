## 1. Base executável e serviços locais

- [x] 1.1 Criar backend/cmd/server com leitura de PORT, NODE_ID e endpoints; verificar configuração válida/inválida e go test ./..., sem adicionar dependências ainda não utilizadas.
- [x] 1.2 Implementar acesso SQL e migrações iniciais de usuários, sessões, pastas, operações, partes, localizações de partes, arquivos, cópias e controle de cluster; verificar aplicação e reexecução das migrações e retry de transação serializável.
- [x] 1.3 Apontar cada backend para seu CockroachDB e MinIO correspondentes; verificar Compose expandido, isolamento dos seis volumes e acesso SQL pelos três nós.
- [x] 1.4 Corrigir bootstrap CockroachDB e MinIO para distinguir partida fria de reinício e validar peers completos; verificar duas inicializações com volumes preservados e reinício degradado sem bloqueio global.
- [x] 1.5 Verificar na versão efetiva do MinIO a persistência local de PUT e leitura com peers isolados, incluindo multipart, checksums e proxy de HEAD; registrar versões e evidência num roteiro de integração antes da implementação de recibos.

## 2. Controle de nós e eleição

- [x] 2.1 Implementar registro de nós, estados e configuração versionada no SQL; verificar identidade duplicada rejeitada e duas alterações concorrentes serializadas.
- [x] 2.2 Implementar concessão temporária de gerenciador e mandato; verificar sucessão após queda e rejeição de escrita por mandato vencido.
- [x] 2.3 Implementar canais internos autenticados, heartbeat e sondagens de backend, SQL e MinIO; verificar que credencial inválida não altera estado e que cada falha parcial produz evidência identificável.
- [x] 2.4 Implementar exclusão automática do tráfego e confirmações obrigatórias em uma mesma configuração; verificar prazo configurado, ausência de aprovação manual e preservação dos volumes.
- [x] 2.5 Implementar bloqueio de atendimento em nó não elegível ou sem autoridade atual; verificar requisições diretas, conexões persistentes e perda de quorum sem sucesso indevido.

## 3. Balanceamento reconciliado

- [x] 3.1 Implementar reconciliador do Nginx com descoberta de controle por múltiplos endpoints; verificar geração, nginx -t e recarga após mudança de membership.
- [x] 3.2 Tratar zero nós elegíveis e configuração indisponível; verificar resposta 503 e rejeição por backend mesmo quando um worker antigo ainda encaminha conexão.

## 4. Identidade e catálogo

- [x] 4.1 Implementar cadastro por login único e senha, hash de senha, sessão compartilhada e logout; verificar cadastro sem e-mail, credenciais inválidas, concorrência e login/logout alternando entre os três backends.
- [x] 4.2 Implementar autorização por proprietário e papel administrativo; verificar acesso cruzado a pasta, arquivo, listagem de transferências, consulta/envio de partes, retomada e cancelamento negado; verificar que novo login do proprietário recupera suas operações e que diagnóstico administrativo não revela conteúdo privado.
- [x] 4.3 Implementar criação e listagem de pastas/subpastas; verificar pai inexistente, pai alheio, nomes duplicados e leitura consistente por diferentes nós.

## 5. Persistência e publicação de arquivos

- [x] 5.1 Implementar operações pending, criação por chave de idempotência do usuário e manifesto imutável de partes/tamanho/SHA-256; verificar repetição igual, conflito de conteúdo/destino, resposta de criação perdida e tamanhos/offsets acima de 2 GiB sem teto associado ao teste.
- [x] 5.2 Implementar envio idempotente de partes para objetos temporários locais e localização persistida por geração de storage; verificar hash/tamanho, parte duplicada/divergente, recebimento por nós diferentes e memória limitada no fluxo Go/S3.
- [x] 5.3 Implementar consulta e reconciliação de partes recuperáveis; verificar cópia preservada, parte perdida, estado desconhecido por falta de autoridade e invalidação após substituição de volume sem descartar partes válidas.
- [x] 5.4 Implementar montagem ordenada em fluxo, validação do checksum completo e escrita explícita local com recibo por site/versão lógica/geração conforme evidência de 1.5; verificar multipart, arquivo vazio, bytes fora de ordem/corrompidos, reinício da montagem e rejeição de confirmação baseada apenas em HEAD encaminhado.
- [x] 5.5 Implementar coleta de todas as confirmações e publicação transacional condicionada à configuração vigente; verificar que pending não aparece no catálogo e que receber partes ou atingir 100% de envio não confirma o arquivo.
- [x] 5.6 Implementar retomada por worker saudável com concessão/geração persistida e consulta de status; verificar queda do coordenador, rejeição de worker antigo, perda de resposta após commit e continuidade da confirmação com navegador fechado.
- [x] 5.7 Revalidar operações quando nó é excluído ou admitido durante upload; verificar conclusão com todos os sobreviventes, espera por nó recém-admitido e impossibilidade de atender partes em nó desqualificado.
- [x] 5.8 Implementar cancelamento idempotente e limpeza segura de temporários; verificar disputa com publicação, finalização atrasada, preservação de partes de operações ativas e versões publicadas, e ausência de expiração por fechamento de página.
- [x] 5.9 Implementar download em fluxo por versão publicada e listagem privada; verificar igualdade SHA-256 pelos três nós, nome original, arquivo vazio, tipo desconhecido e indisponibilidade de pending.
- [x] 5.10 Ajustar transporte Nginx/API para limites por parte com overhead, buffering e timeouts compatíveis; verificar transferência total acima do limite de uma requisição sem rejeição pelo tamanho do arquivo, rejeição de parte inválida e erros de capacidade sem falso sucesso.

## 6. Admissão e recuperação

- [x] 6.1 Implementar associação de nó previamente provisionado com endpoints genéricos e estado joining; verificar entrada de quarto nó vazio sem reiniciar backends existentes.
- [x] 6.2 Implementar sincronização e validação das versões publicadas em nó novo ou recuperado; verificar arquivos ausentes, checksum divergente e geração de volume alterada.
- [x] 6.3 Implementar barreira transacional entre admissão e publicação; verificar upload concorrente com término da sincronização sem admitir nó com arquivo faltante.
- [x] 6.4 Implementar retirada permanente administrativa com etapas de banco e MinIO, mantendo falha temporária separada; verificar que timeout não apaga volumes e que remoção informa impedimentos de quorum/topologia.

## 7. Interface e painel administrativo

- [x] 7.1 Criar frontend React do Acervo e integração com entrada Nginx; verificar login/senha, pastas/subpastas, caminho atual, listagem, identidade visual própria, área administrativa separada e download nativo sem Blob do arquivo inteiro.
- [x] 7.2 Implementar seleção múltipla sem filtro de tipo e fila por arquivo usando File/Blob e XMLHttpRequest, com duas partes de 32 MiB simultâneas no total como default; verificar concorrência limitada, progresso, arquivo vazio e continuidade dos demais após erro de um arquivo.
- [x] 7.3 Criar visão administrativa de componentes, estados, motivo, configuração, cópias e eventos; verificar dados desatualizados identificados e resposta operacional sem nomes, caminhos, conteúdo ou credenciais de uploads alheios.
- [x] 7.4 Implementar controles de registro, acompanhamento e remoção de nó; verificar autorização e uso das mesmas regras do gerenciador.
- [x] 7.5 Implementar injeção e recuperação de falha total e parcial em qualquer nó escolhido, com seleção de backend, banco, storage ou comunicação; verificar rejeição efetiva das operações e detecção normal, sem editar apenas indicadores.
- [x] 7.6 Cobrir pelo painel falha total do nó gerenciador e retorno do nó selecionado; verificar sucessão automática e recuperação sem promoção forçada para ready.
- [x] 7.7 Restringir simulação ao desenvolvimento e manter canal autenticado para desfazê-la; verificar controles inacessíveis a usuários comuns e gerenciamento ativo com painel fechado.

- [x] 7.8 Implementar preparação de manifesto e hash incremental em worker, com buffers limitados; verificar vetores conhecidos, partes finais menores, arquivo vazio e rejeição de arquivo reselecionado com mesmo nome/tamanho mas conteúdo diferente.
- [x] 7.9 Implementar estados Enviando, Confirmando armazenamento e Concluído, além de preparação, espera, nova seleção, erro e cancelamento; verificar que bytes enviados não significam publicação e que cada linha permite ações compatíveis com seu estado.
- [x] 7.10 Implementar retomada automática com página aberta e tentativas espaçadas; verificar perda de resposta de criação por chave persistida, queda durante parte, expiração de sessão e consulta das partes antes de reenvio sem duplicação.
- [x] 7.11 Implementar recuperação da fila pelo backend após reabertura e login, associação de seleção múltipla e validação do conteúdo; verificar retomada sem estado local prévio, operação ambígua, privacidade após troca de conta e confirmação concluída sem pedir arquivo novamente.

## 8. Integração distribuída e entrega

- [x] 8.1 Criar suíte reproduzível com falha total de cada nó e falha isolada de cada componente; verificar prazos, retirada automática e upload/download pelos sobreviventes.
- [x] 8.2 Exercitar partição real e perda de quorum SQL; verificar que lado isolado não atende com estado antigo nem confirma uploads.
- [x] 8.3 Exercitar falha durante cópia, alteração de configuração, queda do gerenciador e recuperação com uploads concorrentes; verificar catálogo e SHA-256 por site, incluindo peers isolados.
- [x] 8.4 Exercitar frontend e painel de ponta a ponta nos cenários das seis specs, incluindo seleção múltipla, erro/cancelamento isolados, queda de conexão, reabertura e falha do coordenador; verificar partes preservadas/perdidas, identidade divergente rejeitada e uma única publicação, distinguindo falhas injetadas, processo morto e rede interrompida.
- [ ] 8.5 Transferir arquivos de 256 MiB e 2 GiB pelo navegador, com parâmetros iguais de partes/concorrência; registrar picos de memória do navegador e backend, confirmar ausência de buffers do arquivo inteiro e verificar SHA-256 final por site; declarar pendente se recursos impedirem execução real.
- [ ] 8.6 Atualizar README e roteiro de demonstração com comandos, identidade Acervo, retomada, contratos de configuração, distinção entre limites por parte e referência de 2 GiB, e limites técnicos comprovados; verificar inicialização pelo scripts/dev.sh, go test ./..., validações do frontend e Docker Compose, sem declarar sucesso para passos não executados.

## Quadro de execução

Política: docs/development-workflow.md. A tabela acompanha as tarefas acima; não é uma
segunda fila. Atualizada pelo coordenador após delegação, integração ou bloqueio.

| Frente | Responsável | Tarefas | Branch/worktree | Estado e próxima ação |
| --- | --- | --- | --- | --- |
| Coordenação | Agente principal | Contratos, integração e testes distribuídos | main / raiz do projeto | 44/46 concluídas; revisando prova do script e coordenando medição de memória |
| Controle e administração | backend_base | 6.1 e 6.4, associação e retirada definitiva | implementation/acervo-cluster-control / /tmp/acervo-cluster-control | Helper concluído e nó restaurado; aguardando próxima atribuição |
| Base SQL | Sem executor ativo | 1.2 | implementation/acervo-backend-sql / /tmp/acervo-backend-sql | Inativa; entrega integrada e validada |
| Uploads | infrastructure | 5.1–5.9 e cópia de recuperação | implementation/acervo-storage / /tmp/acervo-storage | Prova de scripts/dev.sh entregue; executor encerrado, revisão pelo coordenador |
| Frontend | frontend | 8.5, memória em 256 MiB e 2 GiB | implementation/acervo-frontend / /tmp/acervo-frontend | Janela exclusiva para nova baseline de 256 MiB; depois 2 GiB |
| Infraestrutura | Sem executor ativo | 1.3–1.5 | implementation/acervo-infra-runtime / /tmp/acervo-infra-runtime | Inativa; provas integradas e ambiente de teste restaurado |

As demais frentes aguardam as dependências descritas na seção 11 do design.

Em 2026-09-15, a criação de worktrees vinculadas falhou ao criar referências em
.git/refs/heads. As frentes usam clones locais independentes da base 8496c5f em /tmp;
seus diretórios devem ser preservados até integração. Acesso ao socket Docker também
retornou Operation not permitted, inclusive após nova tentativa solicitada pelo usuário.
Validações de runtime permanecem pendentes enquanto o acesso estiver bloqueado.

Entrega 1.1 integrada pelo coordenador em 2026-09-15: configuração de nó e processo
HTTP com liveness, readiness indisponível até admissão real e encerramento por sinal.
Executado na raiz/backend: GOCACHE=/tmp/acervo-go-build GOPATH=/tmp/acervo-go-path
go test ./...; cmd/server e internal/config retornaram ok. Handlers foram exercitados
sem sockets. Encerramento real do processo e integração Docker ainda não foram testados.

### Ponto de retomada antes de reiniciar a sessão

Em 2026-09-15, os dois executores terminaram e suas entregas foram revisadas e
copiadas para a raiz do projeto. Nenhuma entrega depende de processo ativo ou de
arquivo exclusivo dos checkouts em /tmp. O usuário pediu aguardar sua retomada com
bypass antes de lançar novos agentes. Progresso consolidado: 1/46 tarefas concluídas.

- Base: quatro arquivos em backend/cmd/server e backend/internal/config, sem dependências novas.
- Infra: Compose e dois inicializadores revisados, scripts/verify-infrastructure.py
  e docs/infrastructure-verification.md. Acesso SQL local e prontidão independente
  foram configurados; 1.3–1.5 continuam abertas porque exigem serviços reais.
- Verificação consolidada: go test ./... passou nos dois pacotes; o roteiro Python
  passou na expansão Compose, nove cenários MinIO simulados e quatro SQL simulados;
  sh -n e git diff --check passaram. Simulação não comprova consistência distribuída.
- Commit de planejamento: 8496c5f. A implementação está no working tree da raiz,
  sem commit adicional. Preservar essas mudanças ao retomar.

Após o usuário retomar, verificar docker version/docker ps e permissões de Git sem
interferir em serviços existentes. Seguir docs/infrastructure-verification.md em
ambiente isolado para executar os testes reais. Somente então consolidar 1.3–1.5;
atribuir 1.2 para acesso SQL/migrações e liberar frentes dependentes conforme design.
A task 1.1 inclui apenas a base executável: /health/live responde 200 e
/health/ready responde 503 até que o controle real seja implementado.

### Segunda rodada após retomada

Em 2026-09-15, Docker 28.5.1 e criação de worktrees foram confirmados disponíveis;
não havia containers em execução. O usuário retomou e autorizou continuar. As duas
worktrees vinculadas da tabela receberam a base 8496c5f e o mesmo snapshot das entregas
já integradas. Os clones antigos em /tmp não recebem novas alterações.

Infrastructure controla os serviços de teste e fornece SQL ao backend_base; este
usa banco de teste próprio para migrações. O host dispõe de cerca de 2.9 GiB de RAM
livre/disponível e 32 GiB em disco na retomada, portanto limitar caches/concorrência
e preservar os volumes do usuário. Nenhuma tarefa adicional foi concluída só por
liberar permissões.

Validação adicional de 1.1 após liberar permissões: go build gerou binário temporário
em /tmp; processo real em porta localhost livre respondeu live200, ready503 e
uploads404 com SQL/S3 indisponíveis, depois encerrou com código0 por SIGTERM. O processo
de teste foi encerrado; nenhum servidor de desenvolvimento ficou ativo.

Entrega 1.2 integrada: pgxpool, migrador com versão/SHA-256, schema inicial e retry
Serializable somente40001. Na raiz/backend, ACERVO_TEST_DATABASE_URL apontou para
acervo_database_test em localhost27657; go test ./... passou nos três pacotes,
incluindo database em4.310s. Testes reais cobriram migração fria concorrente, reaplicação,
preservação de dados, retry após conflito, rollback/cancelamento e constraints.
Progresso após integração: 2/46. A regra de atualizações periódicas de agentes,
branches/worktrees e status foi registrada em docs/development-workflow.md.

### Integração de infraestrutura e registro/eleição

Em 2026-09-15, integradas 1.3–1.5 e 2.1–2.2. Relatório e roteiros de provas
MinIO em docs/infrastructure-verification.md; ambos os JSON de execução em /tmp
registram complete=true. Na raiz, verify-infrastructure.py passou nos cenários
simulados, sh -n passou e OpenSpec validou. go test ./... com URLs reais SQL e
cluster passou, cluster em 3.512s; database usou resultado em cache dessa execução.
Registro, disputa/renovação de lease, exclusões concorrentes e rejeição de mandato
vencido foram integrados. Progresso consolidado: 7/46. Os novos testes de contas
ainda estão em execução; nenhum endpoint de usuário ou admissão real está pronto.

### Pausa solicitada pelo usuário em 2026-09-15

Não despachar agentes nem continuar implementação até o usuário pedir retomada.
Progresso integrado permanece 7/46, base Git 8496c5f, implementação sem commit.

Na raiz ficaram accounts, catalog e api. O teste real de contas/pastas entre
localhost27657 e localhost27658 passou em4.180s: sessão compartilhada, logout,
expiração, cadastro concorrente, hierarquia privada e conflito de nomes. O teste
HTTP de api passou em0.005s, cobrindo rejeição sem elegibilidade, origem externa,
JSON inválido e sessão ausente. Esses handlers ainda não estão ligados ao main;
verificação transacional de elegibilidade nas mutações ainda não foi implementada.
Não marcar4.1–4.3 concluídas antes de integrar o controle e cobrir os demais critérios.

A worktree /tmp/acervo-cluster-control contém a barreira SQL de6.2/6.3 entregue
em admission.go/admission_test.go, com testes reais em7.027s. Usa recibos fixtures,
portanto não comprova cópia MinIO. Uma nova etapa2.3–2.5 foi iniciada nessa mesma
worktree, envolvendo control e cluster.Guard; revisar seu estado parcial antes de
copiar arquivos. A raiz contém somente a versão anterior integrada de2.1/2.2.

A worktree /tmp/acervo-storage contém storage.go preliminar e alterações exclusivas
de go.mod/go.sum para minio-go/v7 v7.3.0. go test ./internal/storage terminou0 com
[no test files]; faltam revisão, tidy, testes funcionais e integração S3. Não copiar
as dependências sem revisar os upgrades transitivos. Essa entrega não foi integrada.

Os nove containers acervo-infra-runtime ficaram ligados, com volumes preservados.
Não havia teste/build em execução na inspeção da pausa. Os checkouts antigos e a
worktree SQL estão inativos. Na retomada, conferir agentes/processos e git worktree
list antes de redistribuir. Somente o coordenador atualiza os marcadores consolidados.

Ambos os executores confirmaram encerramento sem processos ativos. Controle parcial:
internal/control/controller.go, http.go, cluster/eligibility.go e ajuste em lease.go;
apenas gofmt executado nessa última etapa. Cópias locais duráveis das duas worktrees
com entregas exclusivas e dos relatórios JSON estão em
.git/acervo-pause-2026-09-15, para recuperação caso /tmp seja limpo. Os arquivos .git
das worktrees foram excluídos dos tarballs; restaurar conteúdo em uma worktree válida,
sem substituir referências Git. Nenhuma tarefa adicional foi concluída na pausa.

Em 2026-09-15 o usuário pediu continuar. As duas frentes foram retomadas nas mesmas
worktrees; suspensão anterior encerrada. Coordenador implementa guardas nas mutações
de contas e catálogo e integra main/config após validar controle e storage.

Integração após retomada: control/cluster e storage revisados e copiados à raiz.
Agentes executaram control/cluster com -race em18.805s/13.743s e storage nos três
MinIO em5.309s. Na raiz, go test ./... passou com integração SQL, cluster e contas
habilitadas. Integrações específicas de control/storage ficaram desabilitadas nessa
invocação; suas evidências são as execuções dos agentes. Main agora liga bootstrap,
controle, readiness e API; teste com três processos reais em andamento. A API usa
Guard dentro das transações de conta/sessão/pasta. Teste de rollback após guarda e
sessão entre dois servidores HTTP passou em3.915s no pacote accounts.

Teste scripts/verify-backend-runtime.py passou com três processos Go reais usando
SQL e MinIO locais: admissão, login no1, criação no2, leitura no3, retirada automática
do backend encerrado dentro do orçamento de12s, readmissão e logout cruzado. Todos
os processos de teste encerraram e somente seu schema temporário foi removido.
Pai inexistente também foi coberto no teste real de catálogo. Consolidadas2.3,2.4,
4.1 e4.3; progresso11/46. Falha real de quorum/conexão persistente de2.5 continua
pendente. Teste de uploadsHTTP foi acrescentado e está em execução, ainda sem worker.

Nginx/frontend integrados: imagem acervo-application-lb:test construída com frontend
real. Sete testes Nginx passaram em2.742s, incluindo DNS/TLS/fallback/503. Host inclui
porta original para validar Origin nos POST do navegador. Consolidada3.1;12/46.
TesteHTTP3processos também passou partes distribuídas, cancelamento e projeção admin
com sites,403 usuário comum e ausência de nomes/caminhos privados.

Ambiente acervo-app-test ativo para navegador em localhost28100, portasdiretas28101–3,
SQL dedicado acervo_app_test usando infraestruturaisoladaexistente. Configuração em
/tmp/acervo-application-test.json, gerada por scripts/prepare-application-test.py.
Imagens backend/Nginx construídas; frontend testa cadastro/pastas/painel/partes.
A publicação porworker ainda não está ligada nessa imagem; não declarar uploadfinal
completo nesse estado. Credenciais administrativas locais do teste:admin/admin.


### Publicação, recuperação e limpeza integradas

Em 2026-09-15, scripts/verify-backend-runtime.py passou com três processos reais:
publicação após a última parte, download com bytes iguais nos três nós, privacidade,
cancelamento, retirada automática após encerrar um backend, sincronização no retorno
e logout compartilhado. Processos encerrados e schema de teste removido.

Uploads com SQL e três MinIO passaram com -race em99.207s na worktree do executor.
A entrega inclui limpeza de versões temporárias terminais, preservação de pending
e arquivos publicados, recuperação de parte em outro site após substituição da
geração de origem e prazo de5s nas transações de metadados. O teste de bloqueio SQL
confirmou DeadlineExceeded e passou em26.351s. RunCleanup está ligado ao runtime
de cada nó, com encerramento aguardado junto ao worker.

Uma execução consolidada anterior falhou no teste control: a lease de2s da fixture
venceu sob carga concorrente. A fixture passou a8s, sem alterar parâmetros de
produção; o pacote control real passou em45.218s na repetição. A execução original
não é registrada como sucesso global.

A imagem de teste do backend agora executa publicação/download e falhas didáticas.
O Nginx foi atualizado com os controles de falha. O primeiro teste de navegador
validou cadastro, pastas/subpastas, três arquivos de tipos/tamanhos distintos,
cancelamento isolado e privacidade do painel. O segundo teste está em andamento
para publicação, download, retomada e falha do gerenciador.

Investigação de retirada confirmou via dry-run Cockroach23.2 que3→2 está bloqueado
pela política num_replicas=3, com55 réplicas sem destino. A validação mutável será
4→3. Associação MinIO exige novo site sem buckets e a lista completa de peers.
A implementação de6.1/6.4 está em andamento; nenhuma topologia foi alterada nessa
investigação. Progresso formal permanece12/46 até consolidação dos critérios.


Consolidação após revisão dos critérios:5.1,5.5,5.7,5.8 e5.9 concluídas;17/46.
Manifesto aceita offsets/tamanho de3GiB no teste sem transferir esses bytes; isso
verifica o contrato numérico, não a prova de memória de8.5. Publicação e mudança de
membership foram verificadas com SQL/S3 reais e fixtures de admissão/exclusão.
Cancelamento/limpeza cobrem PUT atrasado e preservação de dados. A raiz acrescentou
ausência de operação alheia na listagem e download vazio nos três sites; os testes
reais de ciclo da operação e publicação/download/recuperação passaram com -race
em26.015s. go test ./... passou após integração, com integrações opcionais
desabilitadas nessa invocação; as execuções reais estão registradas separadamente.


Teste adicional5.3 removeu a versão física de uma parte exclusiva do teste nos três
MinIO, mantendo o recibo SQL. A consulta distinguiu a parte missing da outra
preservada; reenviar somente a parte perdida permitiu publicar. Execução real
com -race passou em15.739s. Em conjunto com unknown, guarda de autoridade e troca
de geração já testados, consolida5.3;18/46. OpenSpec strict e git diff --check passaram.


Frontend: segunda passagem real concluída em
frontend/verification/browser-resume-and-faults.md. Publicação com página fechada,
SHA-256 do download34MiB igual ao original, identidade divergente rejeitada na
retomada, erro409 isolado, storage indisponível e sucessão do gerenciador foram
verificados. Restauração terminou com três nós prontos. Correções subsequentes de
cancelamento de criação rejeitada e tradução de motivos passaram19 testes/build;
essas correções ainda aguardam reteste na imagem atualizada.

Nova rodada: backend_base implementa ciclo durável de topologia; frontend implementa
registro e retirada; infrastructure executa falhas do stack isolado. Raiz prepara
NODE_BOOTSTRAP explícito somente para nós iniciais: novos processos aguardam
registro administrativo, evitando admissão prematura antes da associação MinIO.
Mudança ainda depende da integração do endpoint interno de identidade e teste do
quarto nó. A imagem ativa durante a prova de falhas contém Cleaner e não essas
alterações de ciclo de nós em desenvolvimento.


Consolidação de montagem/validação e interface:5.4,7.1,7.2,7.3,7.6,7.8 e7.9
concluídas;25/46. As evidências incluem testes do worker/S3, barreiras SQL e as duas
passagens de navegador registradas em frontend/verification. Retomada automática
com todas as variações de7.10/7.11, testes grandes e falhas reais completas continuam
abertos. A UI administrativa de ciclo de nós passou25 testes/build, mas7.4 aguarda
integração real da API e quarto nó.

A prova de falhas reais teve duas rodadas incompletas, preservadas pelo executor.
Os cinco modos e stop do nó1 passaram; no nó2 surgiram503 e timeout no sobrevivente
antes de publicar. Nenhum sucesso parcial foi tratado como arquivo disponível.
Restauração terminou com todos os nós prontos. Um A/B de recursos mostrou20/42
sondagens com falha em0.5CPU por Cockroach contra54/54HTTP200 em1CPU. A mediana do
controle caiu2.22s→0.89s, mantendo memória512MiB e timeouts/lease/threshold.
Ainda houve exclusões no começo da faseB, portanto isso não prova estabilidade
completa. O roteiro registra retries idempotentes e prazos, e será repetido.


### Retomada: proteção SQL e integração de topologia

Progresso formal permanece 25/46. Os 15 casos de falha simulada e stop dos três
backends passaram no conjunto das execuções, não em uma única rodada completa.
A partição real do backend revelou transação ociosa segurando o lock exclusivo de
cluster_configuration por mais de dois minutos, após vencer a lease do gestor.
Os timeouts SQL de servidor estavam zerados. A conexão da aplicação agora configura
idle_in_transaction_session_timeout=5s, statement_timeout=5s e transaction_timeout=10s.
Migrações ampliam os dois últimos localmente para 30s. O teste real do banco passou
em 26.944s, liberando o bloqueio abandonado em 5.099849395s e rejeitando o commit antigo.
A partição real ainda precisa ser repetida com a imagem atualizada.

Cluster e lifecycle reais passaram em 38.149s e 55.438s. Control falhou em três
fixtures de prazo: admissão com deadline, limite artificial de 1s para dois passos e
lease HTTP de 100ms. A repetição usa sondagem de 2s/lease de 12s nas fixtures de
controle; a prova HTTP usa lease de 5s. A recuperação lenta agora exige que ambos
os passos retornem e renovem o mesmo mandato com o callback ainda bloqueado.
Nenhum parâmetro do ambiente de aplicação foi ampliado nessa correção.

A raiz integrou a limpeza que evita transações de recibos já ausentes e a guarda
periódica de autoridade durante downloads abertos. Regressão SQL/S3 em execução.
A imagem ativa ainda é anterior à migração 003 e ao ciclo administrativo de nós.

| Frente | Branch / worktree | Estado e próxima ação |
| --- | --- | --- |
| Coordenador | main / raiz | Testes SQL exclusivos, atualização da imagem e repetição das partições |
| backend_base | implementation/acervo-cluster-control / /tmp/acervo-cluster-control | Implementando substituição de storage durante join sem liberar slot nem perder idempotência |
| infrastructure | implementation/acervo-storage / /tmp/acervo-storage | Regressão de controle: nó bloqueado na associação não deve interromper observação dos demais |
| frontend | implementation/acervo-frontend / /tmp/acervo-frontend | README e roteiro da demonstração; navegador e memória aguardam backend |

Worktrees acervo-backend-sql e acervo-infra-runtime estão inativas. Volumes do
ambiente de teste preservados; implementação sem commit. O arquivo de preparo do
quarto nó existe, mas seus serviços ainda não foram iniciados. Testes de 256MiB e
2GiB continuam pendentes; o host tem aproximadamente 16GiB livres em disco.


Atualização do stack isolado: imagem backend topology f18a8b88 construída via
Dockerfile com --network=host após timeout de DNS na rede padrão do builder.
Três backends e Nginx recriados sem alterar serviços de dados ou volumes. Consulta
SQL confirmou migrações 1/2/3 e três nós ready; preflight HTTP somente leitura passou.

Control repetido passou em 93.655s. Regressão completa de uploads falhou em
284.720s: TestRealMetadataDeadline recebeu cancelamento SQLSTATE57014 do próprio
servidor, em vez do DeadlineExceeded esperado; TestRealLostPartCanBeResent encontrou
timeout durante preparação. A expectativa do primeiro foi corrigida para aceitar
ambos os mecanismos de prazo, mantendo o limite de tempo. Ambos estão em repetição.
A otimização de limpeza ainda não é declarada solução de oscilações de saúde.

TestBlockedAdmissionDoesNotStopFailureDetection passou com SQL real em 12.53s,
pacote 13.551s. Nó joining bloqueado permaneceu fora e a falha de outro foi
registrada/excluída no mesmo passo do gerenciador. Imagem atual contém essa correção.
README e docs/demo.md integrados após revisão; comando de build do nginx/README.md
corrigido para usar contexto raiz. Compose validou; testes Python Nginx passaram
com um teste Docker opcional pulado. Nenhum critério novo foi marcado por essas
verificações parciais.

Executores backend_base, infrastructure e frontend concluíram suas entregas e
aguardam; coordenador mantém testes SQL exclusivos antes da janela de partições.


Repetição focada após atualizar o stack passou: limpeza sem transações vazias, prazo
de metadados e parte perdida reenviada, uploads em 62.972s. O prazo foi observado
em 5.024416694s. Join com substituição de volume passou em preflight e storage,
lifecycle em 38.379s, mantendo ID/request/chave e slot até confirmação. Essas
fixtures de lifecycle usam adaptador externo simulado sobre SQL real; não são
prova de associação MinIO real. go test ./... passou sem DSNs opcionais; integrações
executadas estão registradas separadamente.

A API com três processos reais está em verificação. Seu roteiro usa os mesmos
intervalo1s/sondagem2s/lease12s do stack isolado, em vez do intervalo300ms anterior.
Infrastructure prepara a partição do gestor atual para comprovar sucessão; não
executará mutações antes de concluir esta rodada SQL/HTTP.


Runtime HTTP com três processos passou integralmente na repetição, após parar
temporariamente os três backends do stack isolado para não executar dois clusters
de aplicação sobre os mesmos recursos. O cliente do roteiro passou de 4s para 15s,
cobrindo o orçamento das transações; nenhuma política do backend foi relaxada.
A rodada original com timeout de transporte continua registrada como falha.
O finally encerrou os processos, removeu o schema exclusivo e restaurou os três
backends de acervo-app-test.

Cobertura HTTP executada: identidade interna autenticada e sem segredos; somente
admin acessa rotas de topologia; simulação desabilitada retorna404; usuário comum
não acessa pasta, upload, parte, cancelamento ou download alheios; listagem privada
e diagnóstico administrativo sem nomes; novo login recupera operação pending;
publicação/download iguais nos três nós; cancelamento preserva publicado; parada e
readmissão de backend; logout compartilhado. Consolida 4.2 e 7.7: 27/46.

Infrastructure tem exclusividade do ambiente para partições reais na fase network.
Backend_base prepara offline prova de crash de worker com barreira numa parte SQL
exclusiva do teste; frontend aguarda testes de navegador. Nenhuma dessas provas
pendentes foi marcada concluída.


Partições reais v2 passaram em 137.8s, sem ajuste de parâmetros nem cancelamento
manual de sessões. Gerenciador app-node-3 isolado: sucessão para app-node-2 em
18.406s, mandato26→27, upload/download idênticos nos sobreviventes. Cockroach1
isolado: exclusão em 11.556s, backend1 direto503, upload/download nos nós2/3.
Cockroach2/3 pausados: seis requisições diretas, incluindo PUT, retornaram503;
após restauração operação continuou pending, sem parte recebida nem file_id.
Preflight final confirmou três ready; quatro ações do journal restauradas.
Relatórios /tmp/acervo-network-v2-report.json e journal correspondente preservados.

A leitura dos três relatórios anteriores confirmou todos os 15 pares nó/modo de
simulação e stop dos três backends, com rejeição503 na mesma conexão persistente
e transferências pelos sobreviventes. São provas agregadas de execuções distintas,
não uma suíte única completamente verde. Consolida 2.5, 7.5 e 8.2: 30/46.
LB durante quorum e download em voo sob partição não foram medidos nessa rodada;
3.2/8.1/8.3/8.4 continuam abertos nos critérios restantes.

Infrastructure encerrou processos e liberou ambiente. Próxima janela do coordenador:
associação real de quarto nó vazio. Backend_base prepara roteiro de crash do worker
sem mutações; frontend corrige retomada após mais de quatro falhas transitórias e
recuperação da fila independente de metadados locais corrompidos.


Quarto nó real associado no projeto acervo-node4-test, sem reiniciar os três
backends existentes. MinIO inicialmente vazio, NODE_BOOTSTRAP=false, identidade
SQL4 e deployment consultados pelo canal autenticado. Join91153af7-e161-4d74-b22f-8f8ab03052cc
passou preflight/storage, completou e resultou em quatro ready. O arquivo publicado
antes do registro foi baixado diretamente do backend4 com SHA-256
e78f59b4ea1a4cf4f924128a2c5f9c8e69e5b97868a4be550b1dde15d21f008b.

Depois, MinIO4 foi recriado com volume vazio distinto; o volume anterior permanece.
A aplicação detectou deployment novo, mudou geração6264ddff-cdbc-4d59-932c-1efd61caf296
para fef9b5b2-9222-4423-9622-764e032ef44c e bloqueou o nó como storage volume replaced.
Operação automática9cd07948-f45d-45ce-af91-42778157cc6b removeu o peering anterior,
associou o novo e concluiu. Nó4 passou por syncing e voltou ready; SQL confirmou
26 arquivos publicados e26 recibos na geração nova. Download direto repetiu o
mesmoSHA. Em conjunto com validação de integridade/cópia já testada, consolida6.1/6.2:
32/46. Artefatos/identidades do teste em /tmp/acervo-node4-state.json, arquivo0600.

A tentativa de upload concorrente à sincronização não executou: o primeiro comando
falhou por variável não inicializada no roteiro temporário e a repetição perdeu a
janela de syncing. Não é prova de6.3/8.3; os testesSQL de barreira anteriores continuam
registrados separadamente.

Frontend confirmou quatro ready, join e replace completos pelo painel, sem exceções
JS; login inicial503, repetição200. Agora tem autorização exclusiva para retirar
somente app-node-4 pelo painel. Root fará conferência de decommission e recusa3→2.
A UI com retomada prolongada já está na imagemLB b027865a:30testes/build passaram.


Retirada4 via painel concluída, um único POST202, operação
9b8bec55-430d-41f6-b09a-4036af9fdb51. Cockroach node status confirmou nó4
decommissioned/gossiped_replicas0. mc admin replicate info nos três sobreviventes
confirmou somente minio1/2/3; volumes originais e substituto preservados. Os três
processos do projetoacervo-node4-test foram parados após conclusão. A aplicação
manteve o nó4 removed e três ready. Relatório frontend/verification/browser-node-retirement.md
registra etapas, retorno do painel e19respostas503transitórias, sem exceçõesJS.

A primeira consulta preparatória da prova3→2 encontrou503 e não enviou retirada.
Na repetição, POST retornou409 cockroach_topology_blocked; GEToperations confirmou
que a chave recusada não criou operação nem ocupou o slot. Nenhuma retirada3→2
foi iniciada. Consolida6.4 e7.4:34/46.

Os Cockroach sobreviventes estavam consumindo91–96% do limite de1CPU cada após
reconfiguração, com backends abaixo2% e MinIO abaixo5%. Isso motiva medir capacidade
e latência separadamente antes da prova de memória; não altera o resultado de
consistência nem justifica esconder os503observados.


A/B de capacidade após topologia: com1CPU por Cockroach,3/51 sondagens de controle
falharam; mediana1.106s/p952.090s. Com2CPUs,2/63 falharam; mediana0.322s/p951.495s.
As sondagens MinIO não falharam; SQL+CLI não falhou. Throttling por banco caiu de
36–42s acumulados para4–5s na janela de60s. As duas condições executaram o mesmo
observador, incluindo o custo da CLI dentro do container. A melhora não demonstra
ausência total de503. Mantidos2CPUs apenas no ambiente isolado e seus preparadores;
memória512MiB e parâmetros de controle permaneceram iguais.

Preparado teste de256MiB no navegador com medição;2GiB segue pendente com11GiB
livres em disco. O harness de falhas passou a ignorar registros de nós járemoved,
mas continua exigindo exatamente os três nós ativos de teste.


Retomada após consulta de estado: mantidas34/46. Medição de256MiB encerrou com
446amostras e publicação disponível. Picos cgroup dos backends reportados pelo
executor:126.9/131.2/132.8MiB, aguardando revisão do relatório. Duas tentativas de
download nativo cancelaram; logs do Nginx mostraram503 antes de iniciar o corpo
completo, não truncamento de um200. Investigar e conferir hash antes de consolidar
critérios. Nenhum novo upload ou teste destrutivo concorrente à medição.

Revisão do coordenador: samples.summary.json contém446amostras/500ms. PSS máximo
na preparação439.39MiB, baseline máximo406.62MiB; não confundir somaRSS com heapJS.
Backend memory.current máximo126.89/131.21/132.81MiB; memory.peak inclui vida anterior.
ListObjectVersions do prefixo exclusivo do upload mostrou5/6/6versões completas
nos sites,4.25GiB lógicos somados; geração de worker15 e três recibos atuais noSQL.
Disco5.2GiB livres. Nenhuma versão removida. Não ampliar uploads antes de conferir
orçamento. O cancelamento inicial do download corresponde a503JSON do gate e da
preparação; leitura de código não encontrou deadline de10s para o corpo em fluxo.

Integrado scripts/verify-worker-recovery.py. Autoteste offline passou na raiz,
exigindo multipart com bytes e mesma consulta esperando a barreiraSQL antes/depois
da pausa. Execução real ainda não começou.

Terceiro download nativo de256MiB passou em13.715s, arquivo268435456bytes,
SHA-256 a6d72ac7690f53be6ae46ba88506bd97302a093f7108472bd9efc3cefda06484 igual
à origem, conferido também pelo coordenador com sha256sum. Sessão exclusiva
Chromium encerrada; amostras preservadas. Memória limitada real complementa os
testes SQL/S3 anteriores de parte repetida/divergente, recebimento por nós distintos
e geração de storage; consolida5.2:35/46. Critério8.5 continua aberto por verificação
física por site e2GiB não executados.

Backend_base recebeu janela exclusiva para96MiB, piso3GiB livres e crescimento
máximo1.5GiB; cancelamento somente da operação do fixture autorizado ao cruzar
orçamento, com parada dos três backends isolados se cancelamento indisponível.
Volumes e bancos preservados. Demais agentes trabalham offline.

A prova96MiB não comprovou queda durante cópia e não pausou backend: faltou
observação conjunta de multipart e waiter em25s. O fixtureSQL foi restaurado,
mas a operação ficou pending. A disponibilidade caiu porque todos os sites
retornavam XMinioStorageFull; /internal/probe indicava somente storagefalse.
Com4.4GiB livres, até PUT13bytes foi recusado. Nenhuma publicação falsa foi criada.

Recuperação do coordenador: parou os três backends, reservou temporariamente
somente a operação do fixture para impedir novas tentativas, removeu920MiB de
cacheGo gerado nesta sessão, restaurou os backends e cancelou pela API. Duas
respostas503 precederam200cancelled. Uma sessãoSQL temporária exclusiva desse
usuário foi criada para reparar a perda do cookie pelo harness e removida ao final;
isso não conta como prova de autenticação. Journalacervo-capacity restaurado,
três ready, espaço5.6GiB após limpeza de temporários. Todos os volumes preservados.

O teste real de256MiB atravessou o limite100M por requisição usando partes32MiB;
erros de conteúdo/tamanho já exercitados e capacidade real recusou sem publicação.
Adicionado teste unitário de capacidade na escrita/CompleteMultipartUpload, quatro
casos sem recibo e com abort; junto aos cinco casos de conteúdo inválido passou
em0.007s. Consolida5.10:36/46. Novas provas volumosas exigem margem acima da
reservaMinIO; piso3GiB mostrou-se insuficiente e foi abandonado.

Conferência física256MiB passou nos três MinIO com os dois peers desligados em
cada leitura. Tamanho268435456 eSHAa6d72ac7...06484 iguais à origem, versões dos
recibos atuais conferidas. Apps ficaram parados durante leituras para evitar
recuperações; todos os serviços restaurados ao final. Relatório
/tmp/acervo-published-copies-report.json complete=true, detalhes em
frontend/verification/memory-256-result.md. Apenas2GiB permanece pendente em8.5.

Prova scripts/verify-authority-loss.py passou: downloads200 iniciados peloLB
e backend1 antes de pausar Cockroach2/3. Os fluxos terminaram incompletos comEOF
após6.177s/33882112bytes e7.525s/31402920bytes, respectivamente, de268435456
esperados. Nenhum timeout do cliente contado como aborto. Após15s, /api/me peloLB
e pelos três backends retornou503. Os três nós voltaram ready e todas as ações
foram restauradas; relatório /tmp/acervo-authority-loss-report.json complete=true.
Com provas anteriores de expiração/zero membros do Nginx e conexões persistentes
rejeitadas pelos backends, consolida3.2:37/46.

Frontend recebeu janela exclusiva para retomada/expiração com arquivos de até2MiB,
total10MiB; extensão33MiB suspensa enquanto livre<6GiB. Coordenador fará somente
expiraçãoSQL da sessão exata solicitada pelo executor, sem modificar outras contas.

Integrado roteiro do worker revisado: exige7.5GiB iniciais, piso6GiB e crescimento
máximo1.5GiB. Autoteste passou. Falhas comuns também cancelam somente a operação
do fixture; sem confirmação terminal, pausam os backends com journal em vez de
deixar tentativas consumindo disco. Complete só é gravado após finally bem-sucedido.
O fluxo revisado de contenção ainda não teve nova execução real.

Sessão exata do fixture frontend expirada pelo coordenador, após conferir
user_id/token_hash e idade>2s. Uma única linha retornada pelo UPDATE. O navegador
observou GET401, voltou ao login e parou PUT. A restauração sem localStorage
recuperou os mesmos IDs; a contaB não listou operações deA e recebeu404 nas
tentativas de acesso cruzado. Executor ainda termina publicações dos pequenos.

Pedido de decisão enviado ao usuário para eventual recriação SOMENTE dos nove
volumes dos projetos de teste acervo-infra-runtime e acervo-node4-test. Nenhuma
remoção autorizada até receber resposta explícita. Código/specs/relatórios e outros
projetos devem ser preservados. A pergunta não interrompe os testes pequenos em curso.

Usuário autorizou explicitamente apagar os dados de teste. Após o frontend fechar
o navegador e exportar eventos, removidos nove volumes identificados de
acervo-infra-runtime e acervo-node4-test. Evidências copiadas para
/tmp/acervo-evidence-before-reset-20260915; outros projetos preservados. Livre27GiB.
Recriados seis volumes da infraestrutura e banco acervo_app_test; inicializadores
SQL/MinIO executados e três backends responderamready200. Novo clusterSQL
9ca2d79b-8045-43c7-aee5-6b4d1de7411c; nós/arquivos antigos agora são somente
evidência histórica. Nenhum quarto nó foi provisionado nesta base nova.

Infrastructure executa --phase all com relatóriosfresh, antes de arquivos grandes.
Frontend encerrou rodada parcial e encontrou dois defeitos: mensagem503 antiga
reaparecia no login após expiração, e restore inicial da fila não repetia503.
Corrigidos na própria frente e integrados: login limpa erro antigo; restore usa
a espera abortável existente com2/4/8/16s, termina no401 e não repete outros4xx
ou JSON inválido. Trinta e três testes passaram na worktree com dependências;
a tentativa npmtest na raiz falhou por tsx não instalado, sem executar testes ali.
Build do executor passou; imagem ainda não atualizada, retestebrowser pendente.
Relatório frontend/verification/browser-recovery-result.md preserva a conclusão
beta/gamma não executada, sem marcar7.10/7.11 completas antes do reteste.

Suíte fresh --phase all passou em194.52s, exit0:15simulações,3stops, partição
de backend1 e partiçãoSQL1. Vinte casos publicaram e baixaram bytes idênticos
nos dois sobreviventes. Nas15simulações, mesmo socketHTTP respondeu503. Todas
as138requisições de transferência retornaram200/201/204; nenhum retry de503
ou timeout nessa rodada. Exclusão observada1.421–13.405s, incluindo aplicação
da falha e consulta; deadline do roteiro90s. Quorum produziu6/6 respostas503,
operação pending sem parte/arquivo após retorno e cancelamento200.
Journal22/22restaurado, preflight final3ready. Relatório
/tmp/acervo-fresh-failures-report.json complete=true, conferido pelo coordenador.
Consolida8.1:38/46. Backend_base recebeu próxima janela exclusiva para96MiB;
infrastructure prepara sincronização concorrente e frontend aguarda nova imagem.

Segunda tentativa deworker, na basefresh, também inconclusiva: zero amostras
de multipart/contensão. Desta vez cleanup confirmoucancelled/zero arquivos e
restaurou fixture, trêsready. Sem pausas. A investigação posterior da fonte oficial
MinIO identificou errodoobservador: ListMultipartUploads trata prefix não vazio
como chaveexata. O script enviava diretório objects/op/ e agora envia
objects/op/SHA256, exigindo igualdade da chave. Autoteste da correção passou;
nenhuma mudança de aplicação ou timeout foi feita. Próximo reteste aguarda janela.

ImagemLB03629e70474f construída com sucesso e ativada no ambienteisolado. Inclui
limpeza de erroantigo no login e retry da descoberta de fila. Frontend recebeu
janela exclusiva para reteste, incluindo33MiB multiparte; root/infra/backendoffline.
Preparada e integrada prova de publicação durante syncing, dependente de arquivo
96MiB publicado; ainda não executada.


Reteste frontend na base nova aprovado e integrado: arquivo33MiB preservou parte0
após logout, limpeza de estado local e reabertura; enviou somente parte1. Arquivo
pequeno concluiu independentemente. Downloads nativos dos dois arquivos tiveram
SHA-256 igual à origem. Dois503injetados na descoberta da fila foram repetidos
automaticamente; erro503antigo não voltou após login/logout. Relatório
frontend/verification/browser-recovery-fresh-result.md complementa expiração real,
privacidade e rejeição de conteúdo/associação da rodada anterior. Consolida7.10
e7.11:40/46. Backend executa roteiro deworker com chave exata; frontend revisa
8.4 offline; infraestrutura aguarda arquivo publicado e janela exclusiva.


Observador SQL corrigido: UUID textual não aparece como tal em lock_key_pretty.
A sessão exclusiva da fixture, sua transação e a chave binária associam o bloqueio
aos recibos da operação. Tentativa anterior cancelada sem publicação; nova execução
comprovou32MiB em multipart e mesma consulta/transação/lock antes e depois de
pausar o worker. Sucessão e restauração ainda em andamento, sem fechar5.6/8.3.
Verificações offline do coordenador: seis testes do reconciliador e dois do sampler
passaram; OpenSpec estrito e git diff --check passaram.


Prova deworker completa, conferida pelo coordenador:32MiB em multipart, mesmo
waiter antes/depois pausa, sucessor distinto publicou96MiB em33.77s e geração
avançou uma unidade. SHA-256 conferido pelo download; após retorno do antigo e12s
continuava um único arquivo e geração. Journal e fixture restaurados, trêsready.
Relatório /tmp/acervo-lock-worker-report.json complete=true. Junto à publicação
com navegador fechado e repetição pós-publicação SQL/S3 já verificadas, consolida
5.6:41/46. Infraestrutura recebeu janela para publicação durante sincronização.


Usuário liberou espaço para2GiB e autorizou uso. df confirmou133406273536bytes
livres, cerca124GiB, em/var/lib/docker e/tmp no mesmo filesystem. Restrição de
disco anterior resolvida;8.5 segue aberta até executar e conferir a medição.
RAM disponível1943MiB e swap emuso7528MiB: executar navegador em janela exclusiva,
sem builds ou outras provas concorrentes. Nenhuma mudança de limites da aplicação.


Publicação durante syncing comprovada: tiny publicado23:19:02.761027UTC, dentro
do intervalo23:19:00.252712–23:20:45.563022; admissão incluiu geração24. Roteiro
original saiu1 por comparar booleanCSV t com true, preservado complete=false;
parser corrigido. Suplemento confirmou três recibos atuais, recibo3posterior ao
início de sync e três downloadsHTTP200 com52bytes/SHAiguais. Para esses downloads
foi usada sessãoSQLefêmera somente do dono da fixture, apagada nofinally; isso não
é prova de autenticação. Relatórios /tmp/acervo-fresh-publication-sync-report.json
e /tmp/acervo-fresh-sync-supplement.json. Consolida6.3:42/46; conferência física
dos arquivos96MiB/tiny em andamento para8.3.


Cópias físicas96MiB e52bytes verificadas por GET da versão exata dos recibos
SQL em cada site, mantendo os dois peers parados. Seis leituras com tamanho eSHA
iguais, doisrelatórios complete=true e todas as ações restauradas:
/tmp/acervo-fresh-physical-96-report.json e
/tmp/acervo-fresh-physical-tiny-report.json. Com queda durante cópia, sucessão,
configuração/recuperação e publicação concorrente já verificadas, consolida8.3:
43/46. Frontend recebeu janela para primeira sessão65MiB, antes de parada real
do coordenador e perda exata da parte1.8.4,8.5 e8.6 permanecem abertas.


Primeira sessão65MiB encerrada com partes0/1recebidas204 e2nunca recebida.
Helper observou holderapp-node-1 geração72 e parou o processo, removendo somente
versão c8190b13-d3cb-4ace-8704-479f2f048f43 da parte1 da operação
25977d2e-3775-4ce0-ae39-a8c6a0d2a030. API confirmou0available/1missing/2missing,
metadados intocados. Frontend reabre em perfil novo; app-node-1 permanece parado
conforme journal /tmp/acervo-loss-65-node-journal.json até conclusão do navegador.


Retomada65MiB concluída: perfilnovo/login recuperou mesmaop, GET0available/
1missing/2missing, somentePUT1/2, nenhumPOSTcriação. Download nativoSHAigual.
Root executou --recover exit0, trêsreadiness200 e conferência física3sites: cada
MinIOisolado retornou68157440bytes/SHAigual; relatório
/tmp/acervo-loss-65-physical-report.json complete=true. Journals restaurados.
Frontend executa complementosSQL/staleadmin; depois4nóformulário,256/2GiB e8.6.


SQL simulado pelo painel e indicador de dados antigos aprovados; três nós
restaurados, sessão encerrada. Relatório browser-admin-sql-stale-result.md.
Usuário contestou acúmulo de implementação sem commits e autorizou explicitamente
organização em commits incrementais e continuidade das tarefas. Coordenador criou
backup /tmp/acervo-before-commits-4upvhed5 e valida snapshots do index antes de
cada commit, com dependências Go restritas à etapa. Primeiros commits80f3457
(baseSQL/config) e fc75ae5 (cluster/controle/falhas). Testes dependentes de DSNs
foram pulados nesta checagem de commits; evidências reais anteriores permanecem.
Agentes aguardam enquanto histórico é organizado, sem testes de runtime ativos.


Implementação organizada em commits locais por dependência, sem push. Cada etapa
Go foi testada a partir do index exportado, sem depender dos arquivos ainda não
commitados. A API inclui o teste de contas que depende de seus handlers. As três
migrações permanecem juntas conforme contrato do migrador. Dependências foram
normalizadas por go mod tidy em cada snapshot, preservando versões selecionadas.
Frontend em snapshot: npm ci,33testes e TypeScript/Vite passaram, bundleBmyHIFS4
igual ao ambiente real. Compose/shell e6testesNginx passaram;1runtime opt-in pulado.
Autotestes dos fixtures e2testes do sampler passaram. Casos SQL/S3 opcionais não
foram repetidos durante a organização; evidências reais anteriores preservadas.

Commits já criados nesta organização:

- 80f3457 feat(backend): add node configuration and transactional SQL foundation
- fc75ae5 feat(cluster): coordinate membership, manager leases and failure detection
- 07f73e2 feat(storage): stream verified multipart objects to independent S3 sites
- 30bfc4a feat(accounts): add shared sessions and private folder catalog
- e4bcacf feat(uploads): persist resumable parts and fence distributed publication
- 610bf66 feat(admin): persist node association, storage replacement and retirement
- 79d36ec feat(api): expose private transfers and authorized cluster administration
- cb4b57a feat(server): run symmetric backends with recovery and authority gates
- f053efc feat(frontend): add Acervo file browser, resumable uploads and admin panel
- b3bf6ad feat(dev): run independent nodes behind reconciled Nginx routing
- 76af6da test(infra): verify persistent bootstrap and independent MinIO copies
- bfd55e4 test(distributed): prove failover, publication fencing and isolated reads
- 918f42b test(frontend): record resumable uploads, node loss and memory evidence


Organização concluída em14commits, HEADa324cac e worktree raiz limpa antes de
retomar. Sem push. Infraestrutura recebeu janela para provisionar quarto nó, sem
registro viaAPI; frontend fará associação pelo formulário após liberação.
Worktrees dos executores preservadas na base8496c5f; entregas novas serão revisadas
e commitadas pelo coordenador na main, sem resetar seus arquivos.


8.4 concluída, 44/46. Associação pelo formulário, leitura pelo proprietário e
retirada do quarto nó aprovadas. Conferência externa confirmou decommission SQL,
remoção dos peers MinIO e um único arquivo na operação retomada de 65 MiB.
Relatório frontend/verification/browser-node-registration-result.md complementa
as provas de retomada, partes perdidas, SQL e dados antigos do painel.
Infraestrutura entregou startup/restart pelo script real para revisão; frontend
recebeu janela exclusiva para 256 MiB e depois 2 GiB, na mesma imagem.
