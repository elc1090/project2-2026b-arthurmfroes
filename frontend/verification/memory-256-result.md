# Medição real de 256 MiB

Em 15/09/2026, o navegador enviou e publicou `reference-256.bin`, com 268435456
bytes, no ambiente real de três nós. O download nativo completo produziu o mesmo
SHA-256 da origem. O teste de 2 GiB permanece pendente por falta de espaço em disco.
Este resultado não demonstra, sozinho, crescimento constante de memória entre tamanhos.

## Ambiente e método

Entrada `http://127.0.0.1:28100`, sessão exclusiva `acervo-memory-256`, Chromium
142.0.0.0 headless, viewport 1280×720. Imagem do frontend b027865a e backend f18a8b88,
conforme identificação do coordenador. Cockroach estava configurado com 2 CPUs e
512 MiB por banco. Não houve falhas provocadas nem outra transferência desta sessão.

O arquivo de zeros foi criado com `truncate`; o worker real calculou SHA-256 por
partes de 32 MiB. A instrumentação observou no máximo dois XHR simultâneos. Os dez
PUT observados enviaram Blobs de 33554432 bytes cada: oito partes e duas repetições.
O código de hash e o limite da fila não foram modificados para medir.

`sample-memory.py` coletou 446 amostras com intervalo configurado de 500 ms, sem
leituras incompletas. A raiz Chrome era PID 1195808, com perfil exclusivo. RSS/PSS
incluem seus renderers, workers que executam nesses processos, GPU, utility e
zygotes. O cgroup de cada backend forneceu `memory.current` e `memory.peak`.

Um observador da interface classificou as fases aproximadamente a cada 1,97 segundo.
Os limites entre fases têm esse atraso; os máximos abaixo são amostrados, não picos
contínuos. A medição terminou após a conclusão do upload, antes dos downloads.

## Memória observada

Todos os valores estão em MiB. As três colunas finais mostram o máximo amostrado
de `memory.current` dos containers de aplicação 1, 2 e 3.

| Fase | Amostras | Chrome RSS | Chrome PSS | Backend 1 | Backend 2 | Backend 3 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Baseline | 100 | 837,60 | 406,62 | 21,32 | 20,67 | 15,19 |
| Hash | 14 | 870,51 | 439,39 | 20,75 | 19,35 | 14,79 |
| Aguardando | 91 | 772,74 | 342,17 | 109,20 | 131,21 | 19,40 |
| Envio | 92 | 772,02 | 341,13 | 124,02 | 18,44 | 132,81 |
| Confirmação | 136 | 595,85 | 234,05 | 126,32 | 106,16 | 104,14 |
| Concluído | 13 | 566,22 | 199,60 | 126,89 | 86,83 | 16,96 |

A primeira amostra de baseline tinha RSS de 798,11 MiB e PSS de 347,82 MiB.
RSS agregado conta páginas compartilhadas mais de uma vez; PSS reparte essas páginas.
O heap JavaScript da página principal chegou a 4,46 MiB nas consultas disponíveis.
Esse último número não inclui o heap do worker nem substitui a medição dos processos.

Os picos históricos de cgroup já eram 182456320, 165195776 e 159318016 bytes no
baseline e não cresceram nesta rodada. Eles incluem testes anteriores e não são
picos exclusivos deste upload. A memória do container inclui cache, além do heap Go.

## Integridade e falhas transitórias

A criação recebeu dois HTTP 503 antes do HTTP 201. As primeiras tentativas das
partes 0 e 1 também receberam 503; a fila repetiu essas partes e prosseguiu até a
publicação. As consultas de estado capturadas depois disso responderam 200.

Operação `95dae204-18ce-4780-978e-6053e406118f`, arquivo publicado
`5851995e-4177-4331-9f0a-506b8ca03bd3`.

As duas primeiras tentativas de download nativo terminaram com erro no Playwright.
O coordenador conferiu os logs do Nginx: em 22:31:23 UTC houve HTTP 503 com 29 bytes;
em 22:31:51 UTC houve HTTP 503 com 36 bytes. Não houve resposta 200 truncada nessas
tentativas. Após estabilização das consultas, o terceiro download completou entre
22:33:50.616 e 22:34:04.331 UTC, em aproximadamente 13,715 segundos.

`stat` confirmou 268435456 bytes no download. `sha256sum` produziu para ambos:

```text
a6d72ac7690f53be6ae46ba88506bd97302a093f7108472bd9efc3cefda06484
```

O navegador usou o link nativo e `download.saveAs`, sem carregar o arquivo completo
em um ArrayBuffer. A conferência de cada versão final diretamente nos três sites
S3 e dos respectivos recibos fica com o coordenador; este teste do navegador não
substitui essa verificação.

## Artefatos e pendências

Amostras completas, resumo, observações da página, screenshot `completed.png`,
origem e download estão em `/tmp/acervo-memory-256`. O estado de autenticação fica
separado em arquivo privado com permissão 0600, fora do repositório. A sessão Chrome,
o observador e o sampler foram encerrados. O dashboard compartilhado foi preservado.

O teste de 2 GiB não foi executado. Havia aproximadamente 11 GiB livres no início da
janela e o coordenador informou 5,2 GiB ao final. A estimativa de até 24 GiB
transitórios, incluindo versões e cópias, não cabia. A listagem S3 posterior encontrou cinco, seis e seis versões completas do objeto
nos três sites, somando 4,25 GiB lógicos. A operação chegou à geração 15 do worker,
que reescreve os membros a cada tentativa. O catálogo mantém uma única publicação;
essas versões físicas adicionais não aparecem como arquivos duplicados na interface.
Nenhum dado foi apagado.

## Conferência física dos três sites

O coordenador executou `scripts/verify-published-copies.py` após encerrar a medição.
Os três backends foram parados para impedir regravações durante esta conferência.
Para cada MinIO, os outros dois ficaram desligados; um GET da versão exata do
recibo SQL foi lido em blocos de 1 MiB e conferido com SHA-256.

| Site | Versão física | Bytes | Resultado |
| --- | --- | ---: | --- |
| 1 | 2a7ce0ad-090d-4da0-ad34-de5be586b21d | 268435456 | SHA igual à origem |
| 2 | 25488d98-d830-4305-af0c-3b83c4b21d7a | 268435456 | SHA igual à origem |
| 3 | c6f85560-1e13-4aff-a10c-eaf9d9e80ce8 | 268435456 | SHA igual à origem |

As gerações dos três recibos coincidiam com as gerações atuais dos nós. Todos os
MinIO foram restaurados antes dos backends; a verificação final confirmou três
nós prontos e journal restaurado. Relatório `/tmp/acervo-published-copies-report.json`
com `complete: true`. Não foram criados uploads nem removidos objetos nesta prova.
