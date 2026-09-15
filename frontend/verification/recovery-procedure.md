# Retomada, sessão e idempotência no navegador

Roteiro preparado offline para 7.10, 7.11 e 8.4. Ainda não executado. Requer janela
exclusiva liberada pelo coordenador e orçamento para os arquivos. Não iniciar
Docker, SQL ou servidor de desenvolvimento por este roteiro.

Usar `playwright-cli -s=acervo-recovery open http://127.0.0.1:28100` na worktree
frontend e preservar o dashboard 9323. Criar duas contas acadêmicas exclusivas A e B
pela interface. Guardar credenciais e cookies fora do repositório. As operações e
os objetos são reais; somente a perda de transporte indicada abaixo é simulada.

## Fixtures e instrumentação

Depois da liberação, criar dois arquivos de até 2 MiB com nomes diferentes em diretório
privado. O total enviado nesta rodada deve ficar em até 10 MiB. Cada arquivo tem
uma única parte; o limite de 32 MiB continua inalterado. Criar também uma cópia de mesmo nome e
tamanho em outro diretório, com um byte diferente, e um homônimo idêntico em um
terceiro diretório. Registrar `sha256sum` e tamanho de cada um. Não gerar um buffer
JavaScript do arquivo inteiro.

Após login A, instalar o helper sem iniciar uploads:

```sh
playwright-cli -s=acervo-recovery run-code --filename frontend/verification/recovery-transport.js
```

O helper faz `route.fetch` na criação real. Somente após receber HTTP 201 ele aborta
a resposta destinada à página. Registra ID e chave idempotente que o backend criou.
O helper também aborta PUT da parte 0 para manter arquivos incompletos. Demais
respostas e consultas vêm do backend real. A instrumentação fica no processo da
sessão, em `page.__acervoRecovery`; não fica no JavaScript da aplicação.

Para consultar eventos ou liberar a parte 0, usar chamadas `run-code` na mesma
sessão. Não reinstalar o helper. Exportar eventos sem cookies:

```js
async (page) => { return page.__acervoRecovery.events; }
```

```js
async (page) => { page.__acervoRecovery.blockPartIndexes = []; }
```

## Resposta de criação perdida e rede demorada

1. Selecionar os dois arquivos diferentes juntos pelo input real da interface.
2. Confirmar evento `created-response-lost` com HTTP 201 e ID. A próxima criação
   daquele arquivo deve usar a mesma chave e recuperar o mesmo ID. Registrar todos
   os POST e consultar a listagem real para provar que há somente uma operação.
3. Deixar a parte 0 bloqueada por pelo menos 70 segundos. Registrar cinco ou mais
   tentativas fracassadas e os intervalos. A fila deve continuar aguardando, sem
   abandonar após quatro falhas. Este passo simula transporte do navegador, não
   uma partição entre os servidores.
4. Recarregar a página enquanto os arquivos ainda estão incompletos. Esperar a
   listagem real recuperar os mesmos IDs e pedir seleção. Não deve haver PUT
   automático sem acesso aos arquivos locais.

## Armazenamento local limpo e sessão

1. Manter a parte 0 bloqueada. Limpar `localStorage` pelo contexto da página e
   recarregar. Os mesmos IDs devem reaparecer por GET do backend, sem criar outras
   operações nem enviar PUT antes da seleção.
2. Guardar os cookies da sessão de A em arquivo privado 0600. Calcular SHA-256
   do texto do cookie de sessão e informar ao coordenador somente esse hash e o
   userID retornado por `/api/me`. `accounts.go` calcula o hash antes de qualquer
   decodificação base64. Não imprimir o token nem incluí-lo nos eventos.
3. Aguardar pelo menos dois segundos após login e pedir ao coordenador que ajuste
   `expires_at` para `now()-1s` somente da linha com esse userID e token_hash.
   Isso preserva a restrição de prazo posterior a `created_at`. Preservar o cookie e deixar a próxima consulta real receber 401.
   Registrar retorno ao login e confirmar que cessam envios antes de autenticar
   novamente. O agente frontend não executa SQL, não altera relógio nem cookies
   e não falsifica 401. A expiração é do estado real da sessão no backend.
4. Entrar como B. A fila de A deve desaparecer; GET `/api/uploads` deve conter
   apenas operações de B. Consultar por HTTP real os IDs de A usando a sessão B
   deve retornar 404. Registrar apenas status e IDs, sem cookies. Voltar a A e
   confirmar redescoberta das operações originais.

## Seleção múltipla e integridade

1. Após reabertura, usar o seletor de retomada múltipla com um original válido e a
   cópia divergente do outro. O hash completo deve rejeitar somente a divergente.
   A linha correspondente não pode enviar partes nem criar outra operação.
2. Selecionar dois homônimos para uma única linha pendente. A interface deve
   explicar ambiguidade, sem escolher arbitrariamente nem iniciar PUT.
3. Selecionar os dois originais válidos juntos. Liberar o bloqueio da parte 0.
   Para cada PUT, correlacionar ID/índice com o último GET bem-sucedido anterior.
   Partes `available` não devem ser reenviadas; `unknown` exige nova consulta,
   enquanto `missing` permite envio. Distinguir repetições de transporte de uma
   perda real de disponibilidade entre consultas. Guardar o histórico completo.
4. Esperar publicação e baixar os dois arquivos pelo link nativo. Comparar SHA-256
   com a origem usando ferramenta em fluxo. Registrar IDs, duração, status, console
   e screenshots. Não substituir a conferência de cada site S3 pelo download do LB.
5. Sair durante uma espera de rede em operação separada somente se houver orçamento
   autorizado. Confirmar que os POST/PUT cessam após dispose e que novo login
   recupera a operação. Não cancelar dados de outras contas ou rodadas.

## Extensão multiparte condicionada ao disco

O coordenador autorizou futuramente um único arquivo adicional de 33 MiB, com partes
de 32 MiB e 1 MiB, somente após liberar a janela e confirmar pelo menos 6 GiB livres
no início. Se houver menos espaço, executar apenas os casos pequenos e registrar
reaproveitamento multiparte como pendente. Não repetir 256 MiB nem testar 2 GiB.

Depois da autorização, mudar `page.__acervoRecovery.blockPartIndexes` para `[1]`,
enviar o arquivo de 33 MiB e esperar GET real confirmar parte 0 disponível e parte 1
faltante. Reabrir, selecionar o original e liberar parte 1. O histórico deve mostrar
GET com disponibilidade antes de qualquer PUT e nenhum novo PUT da parte 0 enquanto
ela permanecer disponível. Se o backend reportar perda real ou `unknown`, preservar
as observações e aguardar nova consulta; não classificar reenvio como bug sem estado.
Conferir publicação e download nativo por SHA-256. Manter os demais arquivos em até
2 MiB cada e total de pequenos em até 10 MiB. O tamanho de parte da aplicação não muda.

Ao terminar, exportar os eventos, preservar o estado privado se solicitado, fechar
somente `acervo-recovery` e liberar a janela. Falhas, passos não executados e a
comprovação da expiração real devem constar do relatório de execução.
