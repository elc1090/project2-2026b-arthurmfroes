# Retomada após queda real do coordenador e perda de parte

Executado em 15/09/2026, entre 23:24 e 23:28 UTC, no ambiente real de três nós em
`http://127.0.0.1:28100`, imagem frontend `03629e70474f`. A primeira sessão nomeada
foi `acervo-loss-65`; a retomada usou perfil novo em `acervo-loss-65-resume`.

A fixture `loss-65.bin` contém 68157440 bytes, 65 MiB, com partes de 32 MiB,
32 MiB e 1 MiB. O SHA-256 completo calculado em fluxo foi:

```text
25631f11bd18756ec0029380ec886af0c8824dc6b2706bbdb1d9451c7cf45f42
```

Operação `25977d2e-3775-4ce0-ae39-a8c6a0d2a030`, chave
`de5f456c-9624-4b5b-9743-e8dc58be5ce3`, proprietário
`83eaa8b7-6a41-4d9c-874f-d3fb3ce2fe09`.

## Antes da parada

A automação bloqueou somente PUT da parte 2 no transporte do navegador, sem mudar
respostas de disponibilidade. As partes 0 e 1 receberam 204 às 23:24:36.492 e
23:24:38.813 UTC. O checkpoint de 23:24:54.469 exigiu disponibilidade real de ambas,
parte 2 faltante e nenhum 204 para a parte 2. O hash completo e os hashes das partes
no manifesto da API coincidiram com os calculados na origem.

O primeiro checkpoint, consultado enquanto a parte 1 ainda estava chegando,
falhou com `Part 1 is missing, expected available`. Ele não foi considerado sucesso;
a segunda consulta satisfez a barreira. Eventos, origem, manifesto e sessão privada
foram salvos antes de fechar o navegador. Nenhum byte da parte 2 chegou ao backend
nessa primeira etapa.

## Intervenção externa

Com o navegador fechado, o coordenador do teste informou que o holder real era
`app-node-1`, geração 72. O helper parou esse backend e removeu a versão física
exata da parte 1. O coordenador conferiu GET com parte 0 disponível e partes 1/2
faltantes antes de liberar a retomada.

Essa parada de processo e a remoção física foram executadas pelo coordenador,
separadamente da injeção de transporte usada para reter parte 2. Não se deduziu
perda de bytes de um timeout nem se fabricou `missing` no frontend. O journal e a
verificação de versões da infraestrutura complementam os registros do navegador.
O nó 1 permaneceu parado durante toda a retomada e o download abaixo.

## Retomada pelo navegador

A segunda sessão abriu um perfil novo e autenticou a mesma conta, sem carregar
cookies ou localStorage da primeira sessão. GET `/api/uploads` recuperou o mesmo ID
e pediu nova seleção. A chave local criada depois dessa consulta é uma consequência
da descoberta do backend, não um estado restaurado do perfil anterior.

| Instante UTC | Evento observado |
| --- | --- |
| 23:27:00.549 | GET da fila: parte 0 disponível; partes 1 e 2 faltantes |
| 23:27:13.351 | Após hash do original, GET individual confirmou o mesmo estado |
| 23:27:13.504 | PUT da parte 2, nunca recebida antes |
| 23:27:18.614 | PUT da parte 1, recebida antes e perdida durante a parada |
| 23:27:18.616 | Parte 2 respondeu 204 |
| 23:27:20.750 | Parte 1 respondeu 204 |
| 23:27:21.853 | GET: todas as partes disponíveis, publicação ainda pendente |
| 23:27:28.723 | GET: operação publicada; interface Concluído |

Não houve PUT da parte 0, nem novo POST de criação. Cada parte enviada na retomada
correspondia ao estado faltante consultado antes. A ordem 2 antes de 1 é compatível
com os dois XHR globais; o conteúdo final manteve a ordem do manifesto.

Arquivo publicado `87792eb6-3d3b-4e1e-8911-e669c8a50e7a`. O download nativo produziu
`loss-65.bin`, sem erro em `download.failure()`, e `sha256sum` retornou o mesmo valor
na origem e no download. Os bytes não foram materializados em ArrayBuffer integral
no navegador.

O console da segunda sessão registrou somente o 401 esperado de `/api/me` antes
do login. Não houve erro HTTP inesperado nem exceção JavaScript da aplicação.

## Evidência preservada e sequência

A pasta `/tmp/acervo-loss-65` contém `original-manifest.json`, `checkpoint.json`,
`owner.json`, `resume-events.json`, screenshots antes/depois, logs, origem e download.
Conta e sessão privadas ficam em arquivos 0600 dentro do diretório 0700 e não são
incluídas neste relatório.

A segunda sessão foi fechada e a janela liberada para o coordenador restaurar o nó 1,
aguardar readmissão e conferir recibos/bytes nos três sites. O navegador comprovou
retomada da mesma operação e download íntegro nos sobreviventes; a confirmação SQL
de uma única publicação e a verificação dos três sites pertencem ao helper de
infraestrutura. Não atribuir essas verificações ao screenshot da interface.

## Restauração e cópias físicas

O coordenador executou `verify-browser-node-recovery.py --recover`, com saída 0,
e conferiu readiness 200 nos três backends. Depois executou a prova física da
versão publicada: cada MinIO entregou 68157440 bytes com SHA-256 igual à origem
enquanto os outros dois sites estavam desligados. Foram usadas as versões exatas
dos recibos SQL das gerações atuais.

O relatório `/tmp/acervo-loss-65-physical-report.json` terminou `complete=true`;
todas as ações foram restauradas e os três nós voltaram a prontos. A parada
original também está restaurada no journal `/tmp/acervo-loss-65-node-journal.json`.
