# Operações de transferência

`New(ServiceConfig)` recebe pool SQL, nó e storage locais, registro do cluster,
fábrica de storages por nó e verificação externa de elegibilidade. A configuração
não inicia goroutines. O chamador executa `Run(ctx)` e conecta `SyncPublished` ao
fluxo de recuperação do gerenciador.

## Contrato HTTP e autorização

`Create`, `List`, `Get`, `PutPart` e `Cancel` recebem o proprietário autenticado.
As mutações usam `cluster.Guard` antes e depois, na mesma transação. Operações de
storage ocorrem fora da transação e seus resultados são revalidados antes de inserir
recibos. Uma falha de resposta depois do commit é resolvida pela mesma chave de
idempotência do proprietário, inclusive quando a operação já publicou um arquivo.

Create aceita manifesto ordenado e contíguo, SHA-256 hexadecimal, chave UUID e
pasta privada. Tamanhos e offsets são int64 e devem caber nos inteiros seguros da
API JSON. Cada parte pode ter até 32 MiB; o tamanho total não tem teto de 2 GiB.
O manifesto vazio só é válido para arquivo vazio com o SHA-256 correspondente.

Partes retornam `available` e `availability`. Os valores são `available`, `missing`
e `unknown`. Um timeout é desconhecido, não prova de perda. A interface deve aguardar
nova consulta quando encontrar unknown. Versões de gerações antigas não são
consideradas recibos locais válidos. Para uma versão conhecida por recibo válido,
Get e o worker também procuram o conteúdo replicado nos outros endpoints registrados.
Uma leitura por esse caminho não cria recibo de cópia local.

Get verifica abertura e tamanho, sem ler o conteúdo inteiro de cada parte. A
reutilização pelo worker verifica os bytes e o hash da parte. Erros públicos do
serviço são `ErrInvalid`, `ErrNotFound`, `ErrConflict` e `ErrUnavailable`, além de
falhas de autoridade e integridade do storage. A camada HTTP deve sanitizar outros
erros antes de expô-los ao usuário.

## Worker e publicação

Cada worker toma uma concessão SQL de 30 segundos, com geração crescente e
renovação independente a cada 10 segundos. Sucessores não dependem da memória ou
do storage do primeiro coordenador. Uma tentativa antiga não pode persistir progresso
ou publicar com concessão vencida, geração antiga ou operação cancelada.

A montagem lê as partes em ordem e verifica uma parte de até 32 MiB antes de
entregá-la ao fluxo de escrita. Isso permite tentar outra cópia se houver corrupção,
sem emitir bytes inválidos antes da troca. Os buffers dependem do tamanho da parte,
não do tamanho do arquivo. Leituras de cópias corrompidas invalidam somente os recibos
correspondentes observados, sob a concessão do worker.

O adaptador escreve o objeto final explicitamente em todos os sites do snapshot.
Cada recibo é persistido com geração atual do storage, tamanho e hash conferidos.
Antes de publicar, a transação trava a configuração e a operação, exige conjunto
não vazio, nós ready e todos os recibos exigidos pela configuração vigente. Usa
`catalog.LockDirectory` e `catalog.CheckName`, insere um único arquivo e incrementa
`publication_generation`. Admitir um nó durante a cópia exige seu recibo; remover
um nó exige nova avaliação, sem aprovar automaticamente uma cópia que falhou.

Cancelamento e publicação disputam a mesma operação. Cancelar uma operação já
publicada devolve available e conserva o arquivo. Download exige proprietário,
arquivo publicado e recibo do nó local na geração vigente; lê a versão física
referenciada em fluxo.

`SyncPublished` recebe o plano interno do gerenciador e produz novas escritas locais
para o nó syncing. A gravação do recibo verifica estado e geração. A promoção continua
sendo responsabilidade de `cluster.Admit`, que confere os recibos posteriores ao
StartSync e a geração de publicações. O método não força ready.

## Estados e erros de processamento

O worker persiste `content_mismatch`, `name_conflict`, `storage_unavailable`,
`awaiting_parts` ou `capacity_exceeded`. O último só é usado para erros explícitos
`XMinioStorageFull` ou `InsufficientStorage`. Falhas transitórias voltam à fila;
conflitos de nome ou de identidade completa permanecem visíveis para o usuário,
sem repetir a mesma tentativa inválida a cada segundo. Nenhuma mensagem SQL é
persistida como código apresentado ao usuário.

Receber partes não publica o arquivo. Confirming só começa após validar o fluxo
completo em uma primeira escrita final. Sem os bytes necessários, a operação
permanece pending/awaiting_parts para recuperação ou reenvio.

## Verificações executadas

Em 2026-09-15, usando o cluster de teste e os três MinIO em localhost:

```sh
ACERVO_UPLOADS_TEST_DATABASE_URL='postgresql://root@127.0.0.1:27657/acervo_uploads_test?sslmode=disable' GOMAXPROCS=2 go test -race -count=1 -v ./internal/uploads
```

O banco `acervo_uploads_test` foi criado separadamente. Cada teste cria seu próprio
schema e o remove ao terminar. Os objetos usam operações UUID exclusivas. A suíte
terminou com código 0 em 50.536s, incluindo:

- Criação concorrente e idempotente por dois nós; autorização por proprietário.
- Partes recebidas por nós diferentes, repetidas ou com hash inválido.
- Disponibilidade desconhecida, invalidação de geração e leitura por réplica.
- Publicação com três recibos, download pelos três nós e arquivo vazio.
- Recuperação com recibos novos antes da admissão.
- Sucessão da concessão e rejeição do worker antigo.
- SHA-256 completo divergente e cancelamento durante uma tentativa de worker.
- Remoção ou admissão de nó durante a cópia, sem publicação com conjunto antigo.
- Renovação SQL enquanto a cópia permanece bloqueada por mais de dez segundos.

Os testes de membership alteram fixtures SQL; o teste de endpoint indisponível usa
uma fábrica que rejeita a origem após conferir a listagem física da versão na réplica.
Não foram parados containers nesta suíte. As falhas reais de processo/rede e a
integração HTTP/frontend continuam pertencendo às etapas de integração.

## Trabalho ainda pendente

`RunCleanup` repete a varredura local a cada 30 segundos, inclusive para operações
já limpas, pois um PUT cancelado em voo pode criar uma versão órfã depois da rodada.
Somente versões exatas de `parts/<UUID>/` de operações disponíveis ou canceladas
são removidas, após conferir geração e referências. Objetos finais são preservados.
O teste real de cancelamento em voo, órfão tardio, partes ativas e downloads nos três
sites passou com `-race` em 10.134s. A integração do loop ao processo cabe ao coordenador.

Ainda não houve transferência de 2 GiB nem medição de memória do worker/navegador.
A integração com HTTP, eleição real, painel e injeção de falhas cabe ao coordenador.

Após revisão, a busca de partes também conserva key/version históricos quando a
geração do armazenamento de origem muda. A origem substituída sai da busca; versões
físicas em outros sites continuam reutilizáveis, com checksum verificado na leitura
do worker e sem inventar recibos locais. As transações de metadados recebem contexto
próprio de cinco segundos, propagado aos callbacks; esse prazo não envolve o fluxo
S3 do arquivo. Um teste SQL segura o lock da configuração e verifica a interrupção
por deadline. Falha na renovação cancela o contexto da tentativa de cópia.
