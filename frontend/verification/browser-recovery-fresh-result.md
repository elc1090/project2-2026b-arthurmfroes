# Retomada multiparte e correções na base recriada

Executado em 15/09/2026, entre 23:04 e 23:07 UTC, pela sessão nomeada
`acervo-recovery-fresh`, em `http://127.0.0.1:28100`. O coordenador liberou um arquivo
de 33 MiB e arquivos pequenos após recriar a base de desenvolvimento. Havia cerca
de 22 GiB livres. Esta rodada enviou `multipart.bin`, com 34603008 bytes, e
`small.bin`, com 1048576 bytes, usando a mesma seleção múltipla.

A imagem do LB era `03629e70474f`, com asset `index-BmyHIFS4.js`, incluindo as duas
correções de retomada. O backend não foi alterado nesta rodada. Partes de 32 MiB e
dois XHR globais permaneceram como configurados na aplicação.

## Parte preservada e publicação

A automação ocultou uma resposta HTTP 201 real de `multipart.bin`, usando
`route.fetch` e depois `route.abort`. A repetição usou a mesma chave
`5fc692fb-1321-4083-9c62-df6fc5536167` e a mesma operação. A parte 1 foi bloqueada
somente no transporte do navegador; a parte 0 chegou ao backend e respondeu 204
às 23:05:12.584 UTC. `small.bin` concluiu independentemente desse bloqueio.

GETs reais às 23:05:16.769 e 23:05:25.611 UTC confirmaram parte 0 disponível e parte 1
faltante. Depois de logout, limpeza do localStorage, reload e novo login, o backend
recuperou a mesma operação e a interface pediu o original de `multipart.bin`.

Ao selecionar o original e liberar o transporte, o registro mostrou:

| Instante UTC | Requisição ou resultado |
| --- | --- |
| 23:06:21.254 | GET da operação: parte 0 disponível, parte 1 faltante |
| 23:06:21.412 | PUT somente da parte 1, com 1 MiB |
| 23:06:21.693 | Resposta 204 da parte 1 |
| 23:06:21.930 | GET: ambas as partes disponíveis, publicação ainda pendente |
| 23:06:27.460 | GET: operação publicada, interface Concluído |

Não houve PUT da parte 0 após a reabertura. Não houve outra criação de arquivo
durante a retomada. Os bytes chegaram a 100% antes da confirmação final, sem a
interface antecipar Concluído.

| Arquivo | Operação | Arquivo publicado |
| --- | --- | --- |
| multipart.bin | 1b1a9242-17e2-4558-aae1-4af888850c74 | 5ebd7341-d2b6-4f05-b093-f4b7797a8c55 |
| small.bin | 201ca054-adbf-4338-b612-cc300e1102ad | 20431845-75d5-4a55-9ed8-2365cb54edda |

## Downloads

Ambos os links nativos produziram downloads completos, com o nome original e
`download.failure()` nulo. `sha256sum` retornou o mesmo valor na origem e no arquivo
baixado, sem leitura integral em ArrayBuffer no navegador:

```text
multipart.bin  c28a8f34a7efbd4cffe424a21e4a6e4d5bfa8b5daccc381f9eb3c1dc5bac689c
small.bin      30e14955ebf1352266dc2ff8067e68104607e750abb9d3b36582b8af909fcb58
```

A conferência demonstra integridade pelo download do LB. A inspeção física de cada
site S3 é um teste separado do coordenador.

## Correções verificadas

Para o erro antigo no login, a automação injetou uma resposta 503 de `/api/me` antes
do primeiro login. A mensagem apareceu. Depois de autenticar com sucesso e sair,
a tela de login não repetiu a mensagem antiga: a consulta de texto retornou zero
ocorrências. Isso verifica a limpeza de `initialError` em `main.tsx`.

Para a descoberta da fila, a automação injetou exatamente dois HTTP 503 em GET
`/api/uploads`, às 23:05:56.051 e 23:05:58.057 UTC. Sem clicar em Atualizar, a fila
repetiu a consulta após esperas de 2 e 4 segundos; a resposta real 200 chegou às
23:06:03.709 UTC. Os IDs e as partes preservadas reapareceram, mesmo sem estado local.

Os três 503 registrados nesta rodada foram injetados e estão identificados nos
eventos. Nenhum 503 real apareceu no histórico capturado. O 401 de `/api/me` após
logout era real e esperado. O console capturado registrou as falhas HTTP provocadas,
sem exceção JavaScript da aplicação. Nenhuma falha de cluster, processo ou container
foi provocada nesta rodada.

## Evidências e escopo

Eventos completos, screenshot `completed.png`, origens, downloads e console ficam
em `/tmp/acervo-recovery-fresh`. A sessão foi fechada e o ambiente liberado para os
próximos testes. O dashboard compartilhado permaneceu ativo. Não houve alteração
de código durante esta passagem.

Esta rodada completa a prova de retomada multiparte e verifica no navegador as duas
correções do [relatório anterior](browser-recovery-result.md). Expiração real da
sessão, isolamento entre contas e rejeição de conteúdo divergente/ambíguo mantêm a
evidência da rodada anterior; não foram repetidos aqui. Os dados daquela rodada
foram removidos pelo reset autorizado, sem apagar seus relatórios e eventos.

A medição de 2 GiB continua pendente e não pode ser substituída pelo arquivo de
33 MiB. Esta passagem não mediu memória nem simulou partição entre os servidores.
