# Verificação de retomada do worker durante cópia

O teste usa exclusivamente `acervo-app-test`, `acervo-infra-runtime` como infraestrutura existente e o banco `acervo_app_test`. Não cria outra stack. Cria uma conta e uma operação próprias, preservadas ao terminar. A queda é uma pausa do processo coordenador: o processo antigo volta depois para verificar que sua geração obsoleta não publica novamente.

Antes de executar, o coordenador precisa liberar a janela de HTTP, SQL e Docker. A medição de 256 MiB e os testes de topologia não podem estar em andamento. O script compartilha `/tmp/acervo-distributed-failures.lock` com o harness de falhas.

## Preparação sem infraestrutura

```sh
python3 scripts/verify-worker-recovery.py
python3 scripts/verify-worker-recovery.py --self-test
```

Esses comandos não fazem HTTP, SQL ou Docker. O auto-teste verifica manifesto, checksum, journal privado e rejeição de evidência incompleta, inclusive waiter desaparecido após timeout. Não valida a sintaxe dos catálogos SQL nem o comportamento do CLI interativo.

## Executar na janela liberada

Rodar da raiz, usando o `verify-distributed-failures.py` atualizado que ignora o registro do quarto nó já removido:

```sh
python3 scripts/verify-worker-recovery.py --execute \
  --app-compose /tmp/acervo-application-test.json \
  --infra-compose /tmp/acervo-infra-runtime.json
```

Ao rodar de outra worktree, informar o arquivo do harness da raiz com `--harness /caminho/da/raiz/scripts/verify-distributed-failures.py`.

O teste reserva temporariamente somente sua operação, envia três partes de 32 MiB pela API e abre uma transação de fixture que altera `verified_at` do recibo da última parte sem commit. Depois libera a reserva. As primeiras partes podem chegar ao multipart S3 enquanto `readPart` espera a intenção da última parte. A conexão da fixture tem limites próprios e rollback; nenhuma configuração global de SQL é alterada.

A pausa só ocorre após observar bytes confirmados por `ListParts`, operação `pending` e consulta do backend coordenador esperando exatamente o lock dessa sessão. Após pausar, o script exige novamente a mesma consulta, transação e chave de lock. Multipart abandonado, `CreateMultipartUpload` sem partes, timeout, ausência de linhas ou mudança do waiter não comprovam interrupção durante cópia.

Após rollback, exigir outro holder com geração maior, estado `available`, um único registro em `files`, download com tamanho/SHA-256 corretos e os mesmos ID/geração publicados depois de restaurar o worker antigo. O relatório só recebe `complete=true` depois de satisfazer todos esses critérios e concluir o finally de limpeza da fixture e restauração. Falha na limpeza mantém o relatório incompleto.

## Interrupção ou falha da fixture

O journal de containers fica em `/tmp/acervo-worker-recovery-journal.json`. A sessão SQL e a reserva ficam em `/tmp/acervo-worker-recovery-journal.json.fixture.json`. O relatório fica em `/tmp/acervo-worker-recovery-report.json`. Não apagar journals com ações pendentes.

```sh
python3 scripts/verify-worker-recovery.py --recover \
  --app-compose /tmp/acervo-application-test.json \
  --infra-compose /tmp/acervo-infra-runtime.json
```

A recuperação cancela somente a sessão com marcador exclusivo desse teste, libera somente a reserva ainda na geração da fixture e restaura o container registrado, verificando sua identidade. Não cancela uma geração já assumida por outro worker. Não apaga contas, arquivos, volumes ou imagens.

Se a primeira execução falhar por buffering do CLI, sintaxe dos catálogos ou demora da observação, registrar como falha da fixture e corrigir antes de repetir. Não substituir a exigência de waiter vivo por sleep, timeout ou mera presença de multipart. A tarefa 8.3 permanece sem essa prova enquanto o cenário não passar no ambiente real.

## Orçamento de disco

O script monitora o filesystem de `/var/lib/docker` a cada segundo. Recusa iniciar com menos de 7,5 GiB livres (piso de 6 GiB mais margem de 1,5 GiB); interrompe ao cruzar 6 GiB livres ou consumir 1,5 GiB, o que acontecer primeiro. Tentativas podem criar versões completas por site, então 96 MiB não limita consumo total. A medição pertence ao filesystem inteiro, não somente ao teste.

No limite, registra `<journal>.budget.json`, solicita interrupção para rollback e tenta cancelar exclusivamente a operação criada via API. Se o cancelamento falhar, pausa somente `app-node-1`, `app-node-2` e `app-node-3`, com journal antes de cada ação, e deixa a restauração para o coordenador. Bancos, storage e versões existentes são preservados. `--recover` restaura a fixture SQL, mas recusa retomar backends quando há interrupção por orçamento registrada e menos de 6 GiB livres. O finally também preserva pausas registradas abaixo desse piso. Esses limites pertencem somente ao harness isolado, não alteram a política da aplicação nem a reserva interna do MinIO.

O arquivo privado `.fixture.json` preserva o cookie somente da conta do teste para recuperação autorizada após falha. Não publicar esse arquivo. A criação de sessão por SQL para reparar um harness que perdeu o cookie não constitui prova da autenticação da aplicação.

## Primeira execução real — 15/09/2026

A única execução enviou as três partes e estabeleceu o marcador da barreira SQL. Terminou sem evidência suficiente de multipart mais waiter vivo; nenhum backend foi pausado. A versão executada não registrava amostras intermediárias, portanto não permite distinguir ausência de multipart de multipart sem waiter. O relatório continua `complete=false`.

Operação: `87d30178-9c75-4e1a-a997-24a564993843`. Conta: `failproof_24c801c49d6e4936`. O finally restaurou a fixture SQL. A observação posterior encontrou `pending/confirming`, zero arquivos publicados e nenhum multipart com partes na consulta daquele instante. Espaço livre passou de 5,3 para 4,4 GiB.

Como o cookie não havia sido persistido na versão executada, o coordenador autorizou emitir uma sessão temporária somente para o dono dessa operação. As três tentativas de cancelamento retornaram `503 node_unavailable`; a sessão foi apagada em finally. O coordenador assumiu o diagnóstico e confirmou `XMinioStorageFull` em um PUT de 13 bytes com 4,4 GiB livres. O piso antigo de 3 GiB não protegia a reserva do MinIO. Não houve segunda execução.

A versão atual acrescenta journal privado do cookie e eventos separados de multipart/contensão. Essas melhorias e o caminho de orçamento não foram exercitados em nova execução real.

O coordenador recuperou o ambiente: parou os três backends com journal próprio, reservou a operação para impedir novas tentativas, removeu 920 MiB de caches Go gerados e restaurou os serviços. Depois, a API retornou `200` com operação `cancelled`; os três nós voltaram a `ready`. O último espaço reportado foi 5,6 GiB, ainda insuficiente para iniciar este fixture com o piso corrigido. A tarefa 8.3 segue sem prova de queda durante cópia; a limitação de capacidade foi observada de fato.

Qualquer falha ou resultado inconclusivo, mesmo sem disparo do orçamento, tenta confirmar estado terminal da operação própria e cancelar pela API se necessário. Uma operação já publicada é preservada. Se não for possível confirmar que a operação terminou, os três backends são pausados com journal e ficam aguardando o coordenador; o fim do watchdog não autoriza deixar novas tentativas escrevendo. Eventos de contenção informam separadamente `disk_budget` ou `proof_failed_or_inconclusive`.

## Correção do observador multipart

Na release `RELEASE.2025-09-07T16-13-09Z`, um `prefix` não vazio em `ListMultipartUploads` é tratado como nome exato de objeto. A implementação não percorre objetos pelo prefixo parcial: calcula o diretório hash do nome recebido. O observador anterior enviava `objects/<op>/`, portanto consultava um nome diferente do objeto real. [Implementação oficial](https://github.com/minio/minio/blob/RELEASE.2025-09-07T16-13-09Z/cmd/erasure-multipart.go#L237-L274).

O roteiro agora deriva `objects/<op>/<sha256>` do manifesto, envia essa chave completa e exige igualdade de `Upload.Key` antes de consultar partes. Mantém bytes positivos e waiter vivo antes/depois da pausa. Prefixo vazio teria outro comportamento, baseado em cache; não é necessário aqui. [Encaminhamento da API](https://github.com/minio/minio/blob/RELEASE.2025-09-07T16-13-09Z/cmd/erasure-server-pool.go#L1598-L1627).

O auto-teste verifica a construção da chave, o parâmetro enviado e a rejeição de objeto que apenas começa pela mesma chave. Nenhuma nova execução real foi feita para esta correção.

Após o reset autorizado, houve uma segunda tentativa ainda com o observador por
prefixo parcial. Ela terminou com zero amostras de multipart e contenção. A limpeza
revisada cancelou a operação `ceb2e7e3-817b-41c3-91fb-5e3c8680825e`, confirmou
zero arquivos e restaurou a fixture; os três nós responderam ready200. Relatório
`/tmp/acervo-fresh-worker-report.json`, `complete: false`. Nenhum backend foi
pausado. A correção de chave exata acima foi preparada depois dessa tentativa.

## Correção da associação do lock SQL

Na execução com chave exata houve 21 amostras de multipart, incluindo duas partes e 64 MiB confirmados, mas nenhuma contenção aceita. A consulta diagnóstica mostrou que `lock_key_pretty` representa os UUIDs como bytes escapados. Buscar a representação textual do UUID eliminava todos os resultados, mesmo havendo holder e waiters.

O observador agora associa a intenção à sessão exclusiva da fixture e ao seu ID de transação, e junta holder/waiter pela chave binária do lock. Valida também que a fixture pertence à operação consultada. Essa sessão escreve exclusivamente os recibos da última parte dessa operação. Continuam obrigatórios cliente do backend identificado, consulta de partes ativa e os mesmos IDs de sessão, transação, consulta e lock antes/depois da pausa.

## Execução comprovada — 15/09/2026

O relatório `/tmp/acervo-lock-worker-report.json` terminou com `complete=true`. A operação `f4a8f16a-c887-4b8f-949c-9136dd61e7f5` foi interrompida com 32 MiB confirmados em uma parte S3. O observador registrou os mesmos IDs de consulta, transação do waiter, transação da fixture, sessão e chave binária do lock antes e depois da pausa do backend.

O holder mudou de `68d58e92-20cf-44eb-997b-84f995c87366` para `bb082ed1-895a-4922-968c-38dc12cb0da1`; a geração avançou de `2277489351778771375` para `2277489351778771376`. O sucessor publicou em 33,77 segundos após a confirmação da pausa. O download direto do sucessor validou 100.663.296 bytes e SHA-256 `5a310b10d915ba9166406006738169f7e56e1e6525cc8ee0a2e608f013c49d57`.

Depois do retorno do worker antigo e espera de 12 segundos, continuou existindo exatamente um arquivo, ID `c141c5da-38ae-4f72-b6c4-52a2aaa0789b`, na mesma geração publicada. O journal `/tmp/acervo-lock-worker-journal.json` confirmou todas as ações restauradas; a fixture SQL também foi restaurada. A verificação final retornou readiness HTTP 200 nos três nós e 20,93 GiB livres. Nenhum volume ou timeout foi alterado.

Essa execução comprova retomada de uma operação após pausa do processo coordenador durante cópia, com publicação única. Não mede queda por desligamento abrupto do sistema operacional nem ausência de versões intermediárias no object storage; a unicidade verificada é a publicação em `files`.
