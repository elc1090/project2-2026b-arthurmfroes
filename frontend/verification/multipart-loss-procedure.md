# Queda real do coordenador e perda de uma parte

Preparação offline da lacuna composta de 8.4. Nenhuma etapa abaixo foi executada
nesta preparação. Esperar liberação da janela após a prova de sincronização.
Não iniciar Docker, SQL, servidor ou upload antes dessa liberação.

O frontend controla somente o navegador. O coordenador aplica a parada real do
backend titular e a perda das versões físicas de uma parte da fixture, com escopo
exato já autorizado para os dados de teste. Nenhum outro objeto ou volume entra
nessa ação. A perda é provocada no armazenamento, não simulada por JSON na API.

## Preparação após liberação

Usar diretório privado `/tmp/acervo-loss-65` e sessão nomeada `acervo-loss-65`, sempre
no cwd `/tmp/acervo-frontend`, mantendo o dashboard compartilhado em localhost:9323.
Criar a origem sparse `loss-65.bin` com exatamente 68157440 bytes e guardar
`sha256sum`. Guardar também SHA-256 em fluxo das faixas 0–33554431,
33554432–67108863 e 67108864–68157439. Não carregar 65 MiB em um buffer JavaScript.

Criar uma conta exclusiva pela interface e guardar login/senha fora do repositório.
Não usar admin como dono da fixture. Instalar `recovery-transport.js` na sessão com
`playwright-cli run-code --filename`, depois ajustar pela mesma sessão:

```js
async (page) => {
  page.__acervoRecovery.loseNextCreatedResponse = false;
  page.__acervoRecovery.blockPartIndexes = [2];
}
```

Selecionar somente a origem pelo input real. O arquivo produz partes de 32 MiB,
32 MiB e 1 MiB; os limites da aplicação permanecem intactos. O bloqueio da parte 2
é uma falha artificial do transporte do navegador e deve constar do relatório.
Não bloquear partes 0 ou 1 nem injetar perda de resposta de criação nesta rodada.

## Barreira de entrega para o coordenador

Executar `multipart-loss-checkpoint.js` por `playwright-cli run-code --filename`.
O helper faz uma consulta autenticada real, exige operação pending com manifesto
esperado, partes 0/1 disponíveis e parte 2 faltante. Também exige respostas PUT 204
para 0/1 no histórico e nenhuma resposta 204 para 2. Enquanto não satisfizer essa
barreira, não afirmar que uma parte recebida pode ser perdida. HTTP 503 ou `unknown`
exigem aguardar/consultar novamente, sem modificar a evidência.

Salvar o retorno do helper em arquivo JSON de checkpoint sem cookies. Conferir
SHA completo/partes com a origem. Exportar eventos, screenshot e log do navegador.
Guardar `playwright-cli state-save /tmp/acervo-loss-65/session-private.json`, aplicar
permissão 0600 e conferir que existe. O retorno desse comando não deve imprimir os
cookies. Colocar login/senha em outro arquivo 0600 no mesmo diretório.

Fechar somente `acervo-loss-65` com o comando `close`, deixando o dashboard ativo.
Somente depois da confirmação de fechamento, enviar ao coordenador:

- ID da operação e chave idempotente, userID de `/api/me`, nome/tamanho/hash completo.
- Índices, hashes, offsets e tamanhos das três partes.
- Caminhos do checkpoint e estado privado, sem token ou senha na mensagem.
- Confirmação de partes 0/1 com 204 e disponíveis, parte 2 nunca recebida e sessão fechada.

O frontend não escolhe o coordenador do upload pelo gerenciador mostrado no painel.
O coordenador do teste consulta `lease_holder`, geração e prazo no SQL para aquela
operação e identifica o backend real a parar. Se não houver titular observável,
aguardar uma concessão real; não assumir que o backend que recebeu POST é o titular.

## Ações externas e condição para reabrir

O coordenador deve registrar o titular e sua parada real no journal; depois, localizar
as versões exatas da parte 1 e torná-las ausentes em todos os sites que possam
fornecê-las. Manter intactas as versões da parte 0 e os metadados da operação.

Registrar versões e tamanhos antes/depois. Como o MinIO pode encaminhar leitura a um
peer, um GET por proxy não prova persistência local nem perda definitiva. A simples
indisponibilidade de um site pode produzir `unknown`. O frontend espera confirmação
explícita de que a fixture está pronta, o cluster tem autoridade e o coordenador
encerrou a mutação externa antes de reabrir o navegador.

## Retomada e resultado

Reabrir uma sessão nomeada `acervo-loss-65-resume`, com perfil novo, e entrar pela
interface usando a mesma conta. Não restaurar localStorage, para comprovar descoberta
pelo backend. Reinstalar o helper de transporte apenas para registrar eventos e
ajustar imediatamente `loseNextCreatedResponse=false`, `blockPartIndexes=[]`.

Antes de selecionar o arquivo, registrar GET autoritativo com parte 0 disponível,
parte 1 faltante e parte 2 faltante. A interface deve pedir o original, sem criar
outra operação. `unknown` deve causar espera e nova consulta, não reenvio presumido.

Selecionar a origem e conferir:

1. O worker valida o conteúdo original antes de retomar.
2. GET precede PUT 1 e 2; não há PUT 0 enquanto ela permanece disponível.
3. Não há novo POST de criação; ID e chave originais permanecem.
4. Confirmação só vira Concluído após publicação. O coordenador confere uma única
   linha de arquivo e recibos de todos os sites obrigatórios da configuração vigente.
5. Download nativo preserva nome, tamanho e SHA-256. Salvar em arquivo e usar hash
   em fluxo, sem `fetch().arrayBuffer()` do arquivo inteiro.

O coordenador restaura o backend parado e verifica sincronização/readmissão e bytes
nos três sites. O frontend pode observar pelo painel, sem botão de promoção manual.
Registrar gerações do worker, membership e IDs das versões físicas fornecidos pelo
coordenador, mantendo eventos separados de falha injetada, processo morto e perda
física. Partição real já tem prova independente, não foi executada por este roteiro.

## Complementos pequenos do painel

Somente se liberados na mesma janela após estabilizar a fixture, entrar como admin
e provocar falha SQL simulada em um nó escolhido. Registrar POST, indicador de
simulação e observação do gerenciador que exclui o nó inteiro. Restaurar `none` e
esperar recuperação. Não misturar esta simulação com a parada real anterior.

Para dados antigos, bloquear somente GET `/api/admin/cluster` no navegador por mais
de 15 segundos. Capturar aviso de atualização/observação antiga, desbloquear e
confirmar atualização. Essa falha de consulta não representa um componente parado.
Não registrar quarto nó nesta janela; o formulário de associação fica para a rodada
com provisionamento específico pelo coordenador.

Exportar artefatos e fechar somente as sessões desta rodada. Informar liberação da
janela antes de testes concorrentes de worker/sync. Se a perda exata não puder ser
confirmada ou a publicação acontecer antes da barreira, declarar esse cenário
inconclusivo, preservar dados e não criar outro arquivo automaticamente.
