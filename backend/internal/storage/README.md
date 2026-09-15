# Adaptador de armazenamento local

`New(Options)` cria um cliente para um endpoint MinIO específico. Não descobre nós,
não troca de endpoint após falha e não publica arquivos. `Write` recebe prefixo
confiável da operação ou parte, tamanho, SHA-256 esperado e `io.Reader`. Sua chave é
`prefixo/sha256-em-hexadecimal`; conteúdos diferentes não disputam a mesma chave.

O recibo contém `Key`, `VersionID`, `Size` e `SHA256`. A camada de operações deve
vinculá-lo ao nó, geração do storage e operação corretos antes de persistir no SQL.
A versão física pode mudar ao repetir uma escrita idêntica. O recibo não autoriza
publicação nem substitui a verificação da configuração vigente.

Todo objeto não vazio usa multipart de baixo nível, inclusive objetos pequenos.
O adaptador verifica SHA-256, tamanho e EOF antes de completar o multipart. Fonte
curta, bytes excedentes, erro de leitura ou cancelamento não produzem recibo. O
objeto vazio é validado antes de PUT simples. Uma resposta sem versão física é erro.
Falha na resposta de Complete pode deixar uma versão válida não referenciada; o
chamador deve repetir a operação idempotente, sem presumir sucesso nem apagar versões.

As partes são enviadas sequencialmente por `Core.PutObjectPart`, usando
`LimitedReader` e `TeeReader`, sem alocar um buffer do tamanho da parte ou arquivo.
O tamanho preferido é 32 MiB. Arquivos que exigiriam mais de 10.000 partes recebem
partes maiores automaticamente, ainda em fluxo. O limite técnico deste transporte
é 10.000 partes de até 5 GiB cada; um limite menor do servidor continua sendo erro
explícito. Não há teto de 2 GiB nem necessidade de ajuste manual por arquivo.

Erros durante multipart tentam abortá-lo com contexto independente de até cinco
segundos. Falha desse abort acompanha o erro original, sem esconder um temporário
potencialmente pendente. O adaptador não fecha a fonte recebida: o chamador precisa
fechá-la ou desbloquear `Read` quando cancelar uma fonte bloqueante.

`Open(ctx, key, version)` exige a versão física e retorna um leitor em fluxo com
metadados. O chamador fecha o leitor e verifica os bytes quando necessários para
recuperação. Um GET pode usar proxy MinIO, portanto Open nunca gera um recibo.
`Check` escreve e lê um objeto aleatório pequeno e remove somente a versão criada
por esse teste. Ele não examina nem remove arquivos da aplicação.

## Verificação executada

Em 2026-09-15, com os três MinIO da infraestrutura de teste já ativos:

```sh
ACERVO_TEST_S3_ENDPOINTS=http://127.0.0.1:27901,http://127.0.0.1:27902,http://127.0.0.1:27903 GOMAXPROCS=2 go test -race -count=1 -v ./internal/storage
GOMAXPROCS=2 go test ./...
git diff --check
```

A suíte storage terminou com código 0 em 5.309s. Testou objetos vazios, de cinco
bytes e multipart de 8.400.000 bytes em cada endpoint, com tamanho de parte de
5 MiB para exercitar duas partes. Repetiu escritas na mesma chave, obteve versões
distintas e leu ambas com SHA-256 idêntico. Rejeitou hash divergente, bytes excedentes,
fonte curta, erro de leitura e cancelamento; confirmou ausência de objeto publicado
e multipart abandonado nessas falhas. Check também passou nos três sites.

A chave multipart dessa execução termina em:
`adapter-proof/6b43fc6b7ac0dd1d6f34a9d4af1da358/multipart/0267150e49731c0de8bd48d59e4fc51cdee8f1199abd9ba66a521cefec8c161d`.
As primeiras versões retornadas foram `992cbf71-9665-4efc-b6fc-86068f084eba`,
`7bdafa41-bb7f-4568-9f7d-7b45b676daae` e `9cd62924-e9e0-4232-b8b6-b95ac0a9623d`.
Os objetos de teste ficam sob prefixos exclusivos para inspeção. Nenhum serviço
foi parado nessa suíte.

Os testes unitários também cobrem erro de Complete, falha de abort, contexto de
limpeza após cancelamento, versão ausente e cálculo de partes acima de 2 GiB e
acima da capacidade das 10.000 partes de tamanho padrão. `go test ./...` passou
na worktree com as novas dependências. Sem a variável de endpoints, o teste de
integração informa skip. Não houve medição de memória com arquivo de 2 GiB nesta
etapa; isso continua sendo trabalho da integração da aplicação.

Este pacote é parte de 5.2 e 5.4. Ainda faltam os registros de localização no SQL,
montagem das partes por workers, geração de storage, autenticação e publicação
transacional da operação. Nenhuma dessas tarefas foi marcada concluída por esta entrega.

## Cliente e dependências

Foi adicionado `github.com/minio/minio-go/v7 v7.3.0`. Seu go.mod exige as versões
selecionadas de `x/crypto v0.55.0`, `x/net v0.58.0`, `x/sync v0.22.0`,
`x/sys v0.47.0` e `x/text v0.41.0`; pgx permanece em v5.7.6. `go mod tidy` removeu
entradas não utilizadas e classificou minio-go como dependência direta.

A escolha do caminho em fluxo foi conferida no código dessa versão:

- [Core multipart e GET](https://github.com/minio/minio-go/blob/v7.3.0/core.go).
- [Execução HTTP e ausência de repetição de fontes não seekable](https://github.com/minio/minio-go/blob/v7.3.0/api.go).
- [Assinatura HTTP em blocos](https://github.com/minio/minio-go/blob/v7.3.0/pkg/signer/request-signature-streaming.go).

O caminho de alto nível que aloca `make([]byte, partSize)` não é usado. O cliente
foi configurado com uma tentativa por requisição; retomada pertence à operação
persistida, e não a uma repetição oculta do leitor no SDK.
