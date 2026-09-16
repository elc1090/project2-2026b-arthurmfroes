# Recuperação visual e exclusão permanente

Executado em 16/09/2026 na entrada real `http://127.0.0.1:8080`, com os três
backends, CockroachDB e MinIO do `docker-compose.dev.yml`. As sessões Playwright
foram `acervo-delete-proof` e `acervo-orphan-proof`.

## Progresso após perda de parte

O arquivo `acervo-progress-loss-proof.bin` tinha 65 MiB, dividido em duas partes
de 32 MiB e uma de 1 MiB. A automação deixou as partes 0 e 1 receberem `204` e
interrompeu somente o PUT da parte 2 no transporte do navegador. A barra chegou
a `0.9846153846153847`, equivalente a 98% arredondados na interface.

O `backend-node-2` foi pausado sem aviso ao cluster. A versão física exata da
parte 1 foi removida dos storages sem alterar seu recibo SQL. Um observador de DOM
registrou estas transições com o mesmo valor de progresso:

```text
Aguardando conexão ou verificação                 0.9846153846153847
Recuperando partes após falha de um node          0.9846153846153847
Enviando                                          0.9846153846153847
```

A rede registrou outro PUT da parte 1 com `204`. Não houve novo PUT da parte 0.
Depois de liberar o PUT da parte 2 e restaurar o backend, a operação
`10b10e8e-7bb5-4846-acec-69adebd7a10a` chegou a `available/complete` e os três
nós voltaram a `ready`.

## Exclusão durante indisponibilidade

O storage do nó 3 foi pausado e o cluster marcou `backend-node-3` como
`unavailable`. Só depois dessa retirada foi aberto, por um nó sobrevivente, o
download do arquivo `dec0f951-e1f8-4ff8-afcd-b80a0a378d2f`, com 135.266.304
bytes. A exclusão foi confirmada pelo botão da listagem.

- Um download novo recebeu `404`.
- O download aberto antes do commit terminou com status 200 e 135.266.304 bytes.
- Um arquivo vazio com o mesmo nome, `acervo-recovery-proof-2.bin`, foi publicado
  e apareceu como `Concluído`.
- Enquanto o storage estava fora, os dois tombstones mantiveram um recibo pendente
  cada para o nó 3.
- Após o retorno, a observação SQL passou por `unavailable/2`, `syncing/1`,
  `syncing/0` e somente então `ready/0`.

Uma tentativa de controle abriu o download antes de provocar a falha e caiu no
próprio nó cujo storage foi pausado. Esse fluxo foi interrompido pela perda de
autoridade do nó e não conta como prova de concorrência com a exclusão. A rodada
válida acima isolou a exclusão ao abrir o download depois que o nó com falha já
estava fora da rota.

## Versões finais órfãs

A primeira inspeção encontrou versões finais antigas sem recibo SQL, criadas por
tentativas de cópia anteriores. A change foi corrigida para enumerar somente a
chave final canônica e remover todos os seus `versionId`, sem seguir objetos que
apenas compartilham o prefixo.

Na prova posterior, `acervo-orphan-cleanup-proof.txt` publicou a operação
`f8330c3f-afd3-40f1-b15d-e5677dc05140` com três recibos. Uma quarta versão foi
gravada na mesma chave sem recibo. Depois da exclusão:

```text
pending-receipts=0
site-1 versions=0
site-2 versions=0
site-3 versions=0
```

Os arquivos criados pela prova foram excluídos pela própria interface. O arquivo
que já existia antes da rodada foi preservado.
