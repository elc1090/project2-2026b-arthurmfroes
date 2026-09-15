# Falha SQL pelo painel e observações antigas

Executado em 15/09/2026, entre 23:29 e 23:32 UTC, sessão nomeada
`acervo-admin-finish`, em `http://127.0.0.1:28100`, com login administrativo acadêmico.
Não houve upload, parada de processo, alteração de rede Docker ou acesso SQL pelo
executor do frontend.

## Banco local simulado

O seletor do painel escolheu Banco local em `app-node-1`, UUID
`bb082ed1-895a-4922-968c-38dc12cb0da1`. O botão Provocar falha enviou o comando real
de simulação, com resposta 204 às 23:30:19.527 UTC.

A leitura posterior mostrou nó Indisponível, Banco local em Falha, outros componentes
saudáveis e motivo traduzido como Falha na sondagem do banco local. O backend
informou `SQL probe failed` e transição às 23:30:32.218 UTC. A interface identificou
explicitamente Falha simulada: Banco local. O gerenciador inicial era app-node-1,
mandato 11; app-node-3 assumiu o mandato 12 sem comando de promoção no painel.

Restaurar nó respondeu 204 às 23:30:48.432 UTC. O nó retornou Pronto após recuperação,
com transição às 23:31:04.673 UTC. A consulta de 23:31:09 mostrou três nós prontos,
simulação none, configuração v51 e geração de publicações 25.

## Informação desatualizada

Às 23:31:20.079 UTC, a automação passou a abortar somente GET `/api/admin/cluster`
no transporte do navegador. Nenhum componente do cluster foi alterado nessa etapa.
Às 23:31:41.386, a interface mostrou `Dados desatualizados · atualização há 20 s` e
avisou que os dados eram da última consulta bem-sucedida. As observações anteriores
continuaram visíveis, sem representar a falha de consulta como novo diagnóstico
SQL ou storage.

Após remover o bloqueio, o polling recuperou Consulta recente e mostrou novamente
os três nós prontos, com simulação none e configuração v51. A sessão foi fechada e
a janela liberada para o provisionamento do quarto nó.

## Evidências e erros observados

Eventos completos estão em `/tmp/acervo-admin-finish/events.json`; screenshots
`sql-fault.png` e `stale.png` e console ficam no mesmo diretório.

O console registrou um 401 esperado antes do login, um 503 real de
`/api/admin/node-operations` durante a falha SQL e sete erros de transporte
provocados no GET do painel. Não houve exceção JavaScript da aplicação.
Um seletor inicial de automação por label expirou após 30 segundos; usar o papel
combobox resolveu a seleção. Esse atraso ocorreu antes de enviar o comando e não
foi classificado como defeito da aplicação.

O aviso de falha da consulta contém o código `network_error` além do texto em
português. Foi registrado como detalhe de apresentação; não houve alteração de
código nesta rodada.
