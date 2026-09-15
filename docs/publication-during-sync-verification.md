# Publicação durante readmissão

O complemento `scripts/verify-publication-during-sync.py` cobre a corrida real de
6.3/8.3. Sem flags, apenas imprime o plano offline. Usa o harness vizinho e o mesmo
lock/journal. Só executar com a janela de infraestrutura liberada pelo coordenador.

```sh
python3 scripts/verify-publication-during-sync.py --execute
```

Pré-condição: arquivo de pelo menos96MiB já publicado, deixado pela prova anterior.
O roteiro não cria outro arquivo grande. Cria conta própria e um upload minúsculo
com duas partes, retendo a última. Exclui nó3 por falha simulada de storage, restaura
a simulação e observa o estado syncing por SQL no banco exclusivo de testes.

Envia a parte restante apenas depois de observar syncing. Exige snapshots SQL de
mesma transição syncing antes e depois do PUT. Depois, exige arquivo available,
nó3 ready e `published_at` entre o início observado de syncing e a admissão. A geração
sincronizada deve superar a geração anterior à publicação. Confere três recibos
atuais, hash/tamanho, recibo fresco do nó3 e download íntegro nos três nós.

Janela perdida, transição pronta antes da observação, PUT ambíguo ou publicação
fora do intervalo não viram sucesso. Não há repetição automática nem atraso
artificial no backend. O arquivo grande apenas torna a janela naturalmente mais
observável. Snapshots e timestamps ficam no relatório; dados pequenos e objetos
existentes permanecem para diagnóstico. A restauração sempre remove a simulação
e aguarda os três nós prontos, sem apagar volumes ou versões.

```sh
python3 scripts/verify-publication-during-sync.py --recover
```

Caminhos padrão: `/tmp/acervo-publication-sync-report.json` e
`/tmp/acervo-publication-sync-journal.json`.

Verificações da entrega: sintaxe Python e execução sem flags passaram. Nenhum
HTTP/SQL/Docker foi executado nesta preparação; evidência real continua pendente.

## Resultado real em 15/09/2026

O nó 3 iniciou sincronização às 23:19:00.252712 UTC. O arquivo de 52 bytes foi
publicado às 23:19:02.761027, com o nó ainda sincronizando. A readmissão ocorreu às
23:20:45.563022, após 105,31 segundos, incluindo a geração de publicações 24.

O roteiro original saiu com erro ao comparar o booleano CSV `t` com `true`. Seu
relatório permanece `complete=false` em `/tmp/acervo-fresh-publication-sync-report.json`.
As duas comparações foram corrigidas para aceitar as representações verdadeiras.
A falha não foi repetida: os timestamps e snapshots da mesma rodada preservam a
evidência de sobreposição entre publicação e sincronização.

O suplemento `/tmp/acervo-fresh-sync-supplement.json` confirmou os três recibos
atuais, o recibo novo do nó 3 e downloads HTTP 200 pelos três nós com tamanho e
SHA-256 iguais. Como o roteiro não preservara o cookie, foi criada uma sessão SQL
efêmera somente para o dono dessa operação e removida no finally. Esses downloads
comprovam leitura autorizada dos bytes; essa sessão não comprova autenticação.

Depois, `/tmp/acervo-fresh-physical-tiny-report.json` confirmou a versão exata do
recibo de cada site, mantendo os outros dois MinIO desligados. Todos retornaram
52 bytes e SHA-256 `f8c84b514945d7badda229ba78dc922340b3c28c863af427f3641db9cd9ea63d`.
Os serviços foram restaurados e os três nós terminaram prontos.
