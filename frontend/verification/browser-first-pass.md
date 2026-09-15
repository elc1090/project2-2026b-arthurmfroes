# Primeira passagem real pelo navegador

Executada em 15/09/2026, sessão Playwright CLI `acervo-frontend-e2e`, Chromium
headless, entrada `http://127.0.0.1:28100`. A entrada usou Nginx, três backends,
CockroachDB e MinIO reais do projeto Compose de teste `acervo-app-test`.
O worker de publicação ainda não estava integrado nesta passagem.

## Resultado

- Cadastro `browser-first-20260915` retornou 201 e login retornou 200.
- Pastas `Pesquisa/Capitulo` criadas pela interface, 201 em ambas. As migalhas
  mostraram `Meus arquivos / Pesquisa / Capitulo`.
- Seleção única com três arquivos: `alpha.unknown`, 21 bytes; `empty`, vazio;
  `bigpart.bin`, 34 MiB. A preparação executou o worker real do navegador.
- O arquivo de 34 MiB produziu partes de 32 MiB e 2 MiB, ambas recebidas com 204.
  `alpha.unknown` produziu uma parte com 204. Vazio produziu manifesto vazio.
- A interface mostrou os três como `Confirmando armazenamento`, 100%, sem declarar
  conclusão nem listar arquivos ainda pendentes.
- Cancelar `alpha.unknown` retornou 200 e mostrou `Cancelado`. As outras duas
  transferências permaneceram em confirmação.
- Logout retornou 204 e removeu todas as linhas de transferência da interface.
- Usuário comum não recebeu botão administrativo; consulta direta ao endpoint
  administrativo retornou 403.
- Login administrativo e painel retornaram 200. O painel mostrou três nós,
  configuração v3, gerenciador app-node-2, mandato 1, duas operações pendentes
  identificadas por UUID e três sites pendentes por operação. Não mostrou nomes
  dos arquivos do outro usuário. Observações nulas foram identificadas como
  `Sem observação disponível`, sem inventar saúde dos componentes.

## Integridade observada no protocolo

SHA-256 informado pelo worker e aceito pelo backend:

| Arquivo | SHA-256 |
| --- | --- |
| alpha.unknown | f336269a61927c7a4b040f832507fb88ce0c8e6c7cf294d0dfb2ea47e828e383 |
| empty | e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855 |
| bigpart.bin | 08d042084174faf6d0ec760db2be015ca3b3e4211e7677ac23a7a5c49680c9a6 |

Esta passagem não verificou objeto final ou download porque a publicação ainda
não estava disponível. Também não mediu limite de concorrência em rede nem memória
real dos processos; esses critérios não se deduzem do recebimento bem-sucedido.

## Console, rede e imagens

Console sem exceções JavaScript. Os três erros de recurso foram: 401 da consulta
inicial sem sessão, 403 do teste administrativo proibido e um GET de upload com
503 transitório. O GET seguinte retornou 200 e a fila recuperou automaticamente.

Evidências locais da sessão:

- `/tmp/acervo-first-transfers.png`, tela com as três transferências em confirmação.
- `/tmp/acervo-first-admin.png`, painel administrativo, imagem inspecionada.
- `/tmp/acervo-frontend/.playwright-cli/console-2026-09-15T16-48-28-936Z.log`.
- Comandos `playwright-cli -s=acervo-frontend-e2e requests` e `console` retornaram
  a sequência descrita acima. Arquivos selecionados em `/tmp/acervo-browser-files/`.

Um primeiro comando de automação tentou usar `Buffer` dentro do contexto restrito
`run-code` e falhou antes de selecionar arquivos. Foi corrigido criando os arquivos
no disco e usando `setInputFiles` com seus caminhos. Não foi falha da aplicação.

## Correção após revisão

O progresso de uma operação publicada agora é sempre 100%, mesmo se os recibos de
partes temporárias já não forem consultáveis. A regressão está coberta no teste da
fila reaberta. A nova versão depende de reconstrução da imagem da interface para
ser exercitada no navegador.
