# Publicação, retomada e falhas pelo navegador

Executado em 15/09/2026, entre 18:02 e 18:09 UTC, Chromium headless pela sessão
Playwright CLI `acervo-frontend-e2e`, entrada real `http://127.0.0.1:28100`.
O ambiente já incluía workers de publicação, download e simulação de falhas.

## Publicação e retomada

Ao entrar novamente em `browser-first-20260915`, o arquivo vazio e `bigpart.bin`
de 34 MiB enviados na primeira passagem estavam publicados. Ambos mostraram
`Concluído` e progresso 1. A publicação ocorreu com aquela sessão anterior fechada.

O download nativo de `bigpart.bin` preservou o nome e foi salvo em
`/tmp/acervo-browser-files/download-bigpart.bin`. `sha256sum` no original e download
retornou, em ambos, `08d042084174faf6d0ec760db2be015ca3b3e4211e7677ac23a7a5c49680c9a6`.

Para `resume.bin`, também de 34 MiB, a automação abortou somente a requisição da
parte 1 usando `page.route`, simulando desconexão do transporte no navegador.
A interface mostrou espera por conexão. Após recarregar, recuperou a operação pelo
backend e pediu nova seleção. Uma cópia com mesmo nome e tamanho, mas primeiro byte
alterado, foi rejeitada pelo worker com a mensagem de conteúdo divergente.

Selecionar o original retomou a mesma transferência e concluiu. O download nativo
foi salvo em `download-resume.bin` e comparado com `sha256sum`, novamente com o hash
acima em ambos. Esta passagem não mediu a contagem exata de reenvios de partes.

Selecionar juntos `resume.bin` já existente e `independent.txt` demonstrou erro
isolado: a criação repetida por nome retornou 409, enquanto `independent.txt`
foi publicado e mostrou `Concluído`.

## Simulação pelo painel

Todos os comandos de falha usaram os botões do painel e a API administrativa.
Nenhum container foi parado nem rede Docker alterada nesta rodada.

1. O painel inicialmente mostrou três nós prontos, configuração v3, geração de
   publicações 4, gerenciador `app-node-3`, mandato 3.
2. Falha de storage em `app-node-1` produziu estado `Indisponível`, componente
   storage em falha e motivo de sondagem do armazenamento. Os demais componentes
   observados permaneceram saudáveis.
3. `Restaurar nó` enviou `none`. Eventos registraram exclusão, início de sincronização
   e readmissão. O nó voltou pronto com geração sincronizada 4, configuração v5.
4. Falha total do gerenciador `app-node-3` produziu sua exclusão e configuração v6.
   `app-node-2` assumiu o mandato 4 automaticamente. O painel distinguiu a falha
   simulada da observação dos componentes, sem inventar estado de SQL/storage.
5. Restaurar `app-node-3` passou pelo fluxo de recuperação. Ao final, os três nós
   estavam prontos, simulação `none`, configuração v7; o nó restaurado apresentava
   geração sincronizada 4 de 4 e o gerenciador continuava `app-node-2`, mandato 4.

Imagem local: `/tmp/acervo-manager-fault.png`. Console local:
`/tmp/acervo-frontend/.playwright-cli/console-2026-09-15T18-02-56-419Z.log`.
O console não registrou exceções JavaScript. Registrou o 409 provocado e respostas
503 transitórias de consultas de upload/painel, incluindo durante a sucessão.
Consultas posteriores recuperaram os dados. A sessão foi encerrada ao final.

## Correções preparadas após esta passagem

- Cancelar uma linha cuja criação foi rejeitada por 400/409 agora repete a criação
  idempotente, consulta a chave no backend e, se não existir operação, encerra
  somente a linha local. Não cancela o arquivo existente de outra chave.
- O painel traduz os motivos de sondagem de backend/controle e o evento de mudança
  de simulação, mantendo desconhecidas as observações que o backend não possui.

`npm test`: 19 testes passaram. `npm run build`: TypeScript e Vite passaram.
Essas duas correções ainda precisam da imagem atualizada para reteste no navegador.

## Pendências desta rodada

Arquivos de 256 MiB e 2 GiB não foram enviados. A rodada aguarda integração da
limpeza e orçamento de disco/memória combinado com o coordenador. Ainda não há
medição de picos dos processos, teste de todos os componentes pelo painel,
partição real ou processo morto nesta evidência. Não se substituem esses critérios
pelos cenários menores executados aqui.
