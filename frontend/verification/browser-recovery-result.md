# Retomada e expiração real de sessão

Executado em 15/09/2026, entre 22:45 e 22:53 UTC, sessão nomeada `acervo-recovery`
no ambiente real `http://127.0.0.1:28100`. Foram selecionados três arquivos de 1 MiB,
abaixo do orçamento de 10 MiB. Não houve falhas provocadas no cluster.

## Resultados

- A resposta HTTP 201 real de criação de `beta.bin` foi ocultada da página com
  `route.fetch` seguido de `route.abort`. A tentativa seguinte usou a mesma chave
  `74c726ce-7585-4b58-9d88-a3d847a3b32d` e recuperou a mesma operação. A listagem
  posterior tinha somente uma operação para esse arquivo.
- PUT da parte 0 foi abortado no transporte do navegador. Beta fez sete tentativas
  entre 22:46:55.735 e 22:48:45.170 UTC, por mais de 109 segundos. Gamma também
  continuou depois da quarta tentativa. A interface permaneceu aguardando conexão;
  cada tentativa observada consultou o estado real antes do PUT.
- O coordenador expirou somente a sessão da conta A no SQL, com prazo final
  `2026-09-15T22:48:45.472136Z`. O cookie foi preservado. GET da operação beta
  recebeu 401 às 22:49:01.752 UTC; a interface voltou ao login e interrompeu a fila.
- Depois de limpar `localStorage` e autenticar novamente, GET `/api/uploads`
  retornou os mesmos três IDs. Alpha estava concluído; beta e gamma pediram seleção.
  Recarregar a página preservou esse comportamento, sem novos POST de criação.
- Selecionar dois arquivos homônimos de gamma produziu mensagem de associação
  ambígua, sem escolha automática. Selecionar juntos gamma original e beta com um
  byte diferente fez gamma continuar e rejeitou somente beta por conteúdo diferente.
- A conta B não mostrou a fila de A. Sua listagem respondeu 200 com `operations:[]`.
  GET, PUT de parte e cancelamento por ID direto da operação beta responderam 404,
  usando autenticação real de B. Nenhum desses comandos alterou a operação de A.

As operações observadas foram:

| Arquivo | Operação |
| --- | --- |
| alpha.bin | 14f6a577-e7d5-4d67-bb84-c094131feffe |
| beta.bin | c83f6fb1-e38b-4d79-98b3-22ce39c4a701 |
| gamma.bin | cc0c47df-8047-404f-bdab-fef05f6643d2 |

Conta A `3cefb371-0e84-48e9-94cf-e169f2cd2734`. Tokens e senhas não fazem parte
deste relatório. O coordenador conferiu que a alteração SQL afetou uma única sessão.

## Falhas encontradas e limites

Na preparação, o helper usou `URL`, ausente no contexto do `run-code` desta versão
do Playwright CLI. Corrigi o helper para extrair o caminho sem esse global. Alpha
escapou do bloqueio durante a reinstalação e concluiu; não conta como prova de resposta
perdida. Beta e gamma foram os arquivos usados nos cenários de transporte. Os erros
dessa preparação foram da automação, não exceções da aplicação.

Ao expirar a sessão, o login reapresentou um erro 503 antigo da consulta inicial de
`/me`. A correção mínima preparada limpa esse erro quando um login tem sucesso.
Ela foi feita após encerrar a sessão e ainda não foi verificada no navegador.

Na saída de B, duas tentativas de logout receberam 503. A terceira respondeu 204,
seguida de login A com 200. Porém GET `/folders` e GET `/uploads` responderam 503
às 22:52:24 UTC. A recuperação inicial da fila não repetiu a consulta, e o botão
Atualizar da página só consulta pastas. Isso impediu concluir beta/gamma nesta rodada.
A correção offline autorizada repete falhas de rede e 5xx na descoberta da fila
com espera de 2, 4, 8 e até 16 segundos, interrompida por dispose. HTTP 401 encerra
a sessão; outros 4xx e JSON inválido continuam definitivos. Ela reutiliza a espera
abortável da fila e ainda não foi verificada no navegador.

Após as duas correções, `npm test` passou com 33 testes, incluindo seis falhas
transitórias antes de restore bem-sucedido, dispose durante espera e 401/403/409/JSON
inválido sem retry. `npm run build` e `git diff --check` também passaram.

A sessão foi fechada e a janela liberada para o reset de volumes autorizado pelo
usuário. A conclusão com os originais válidos após todos esses passos e seus downloads
ficou pendente. A retomada após seleção válida já tinha evidência anterior, mas não
substitui essa parte não executada da rodada atual. Nenhum novo teste começou após
o pedido de encerramento.

O caso multiparte de 33 MiB não foi executado: havia 5,6 GiB livres, abaixo dos
6 GiB exigidos pelo coordenador. Cada arquivo desta rodada tem uma única parte;
não se demonstrou reaproveitamento de parte intermediária de um arquivo multiparte.
O teste de 2 GiB também continua pendente.

Eventos completos e screenshot ficam em `/tmp/acervo-recovery/events.json` e
`/tmp/acervo-recovery/final.png`; logs da sessão ficam na pasta `.playwright-cli`
da worktree. O arquivo de sessão privado foi preservado fora do repositório com
permissão 0600. Os eventos exportados não contêm cookies. O dashboard 9323 permaneceu
ativo, e nenhum container, rede ou volume foi alterado pelo agente frontend.

A passagem seguinte na base recriada concluiu o caso multiparte e verificou as
duas correções no navegador. Consulte [o relatório da base recriada](browser-recovery-fresh-result.md).
