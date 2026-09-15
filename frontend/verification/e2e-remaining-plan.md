# Lacunas de ponta a ponta e orçamento de 2 GiB

Revisão offline de 8.4, feita em 15/09/2026. Não executou navegador, Docker, HTTP ou
SQL e não modifica o estado das tarefas. A leitura incluiu as seis specs, os
relatórios do frontend, os relatórios de infraestrutura e a implementação de escrita
versionada e limpeza de partes.

## Cobertura que já pode ser reutilizada

| Critério | Evidência existente |
| --- | --- |
| Login, pastas/subpastas, migalhas, tipos desconhecidos e vazio | browser-first-pass.md e browser-resume-and-faults.md |
| Seleção múltipla, erro por arquivo, cancelamento independente | browser-first-pass.md e browser-resume-and-faults.md |
| Resposta 201 perdida, mesma chave/operação, espera após quatro falhas | browser-recovery-result.md |
| Expiração real SQL, novo login, localStorage limpo, isolamento entre contas | browser-recovery-result.md |
| Ambiguidade e mesmo nome/tamanho com conteúdo diferente | browser-recovery-result.md e browser-resume-and-faults.md |
| Parte preservada após reabertura, GET antes do PUT faltante, publicação/download | browser-recovery-fresh-result.md, com 33 MiB |
| Confirmação com navegador fechado | browser-first-pass.md seguido de browser-resume-and-faults.md |
| Painel: storage simulado, gerenciador em falha total, sucessão e readmissão | browser-resume-and-faults.md |
| Acompanhamento de associação/substituição e retirada real pelo formulário | browser-node-retirement.md |
| Processos mortos, partição real e perda de quorum | docs/infrastructure-verification.md e docs/authority-loss-verification.md |

As provas HTTP de infraestrutura complementam o navegador. Não é necessário repetir
os quinze casos de falha por botões para provar novamente a matriz de componentes.
Porém elas não comprovam o cenário composto abaixo, em que a mesma fila do navegador
atravessa a queda do nó coordenador e perda real de partes.

## Pendência central de 8.4

Falta a cadeia única de `development-environment / Reabertura e falha de nó`:
fechar o navegador durante envio, falhar o coordenador real daquela operação,
reabrir, aproveitar partes preservadas, reenviar uma parte antes recebida mas agora
perdida e confirmar uma única publicação. Hoje há evidências separadas de cada
família, mas o relatório fresh só bloqueou uma parte que nunca chegou ao servidor.
Isso não equivale a perder uma parte que já havia recebido 204.

Plano mínimo para uma janela coordenada:

1. Usar uma conta exclusiva e um arquivo de 65 MiB, mantendo partes de 32 MiB,
   32 MiB e 1 MiB. Reter a parte 2 no transporte do navegador. Receber 204 das
   partes 0 e 1 e registrar seus recibos, versões físicas e locais.
2. Registrar por SQL o titular real da concessão da operação. Esse coordenador do
   upload pode ser diferente do gerenciador do cluster. Fechar a sessão do usuário.
3. O coordenador do teste para o processo backend titular pelo harness com journal.
   Não usar uma simples resposta 503 injetada como prova de processo morto.
4. Para demonstrar perda definitiva de uma parte, o teste de infraestrutura deve
   tornar ausentes todas as cópias físicas da versão da parte 1 desta fixture,
   preservando parte 0. Essa mutação exige autorização e escopo exato por
   operação/chave/VersionId; não apagar bucket ou volume compartilhado. Apenas parar
   um MinIO pode resultar em `unknown` ou permitir leitura por proxy e não prova
   perda. Registrar a enumeração de versões e os resultados locais antes/depois.
5. Reabrir e entrar na mesma conta. Exigir GET autoritativo com parte 0 disponível,
   parte 1 faltante e parte 2 faltante. Se vier `unknown`, observar espera e novas
   consultas; não fabricar `missing` no cliente nem declarar perda demonstrada.
6. Selecionar o original. O histórico deve conter PUT 1 e 2 após a consulta, sem
   PUT 0 enquanto ela continuar disponível, sem novo POST de criação e com a mesma
   operação. Consultar uma única linha de arquivo publicada, baixar com SHA igual
   e conferir versões/recibos dos sites obrigatórios.
7. Restaurar o backend, esperar sincronização/readmissão e conferir os bytes nele.
   Guardar journal, IDs, mandato do worker antes/depois, estado do painel e eventos.

Três partes permitem demonstrar simultaneamente parte preservada, parte perdida e
parte nunca enviada. Dois arquivos menores não exercitam essa distinção dentro da
mesma operação. O arquivo de 96 MiB das provas do backend não deve ser reenviado só
para aumentar duração; a janela pode reutilizar seu mecanismo de observação de lease.

A partição real já tem prova separada no harness. Se o critério do revisor exigir
interação do frontend durante uma partição, acrescentar uma leitura/download pequeno
na sessão do usuário enquanto o harness isola o coordenador, sem novo arquivo grande.
Identificar esse caso como rede interrompida, separadamente da parada de processo e
dos bloqueios artificiais por `page.route`.

## Pontos do painel sem interação completa registrada

- O formulário de registro de um nó foi implementado, mas a associação do quarto
  nó foi feita pela API do coordenador. O navegador apenas acompanhou o resultado.
  Para cobertura do formulário, o coordenador provisiona um quarto nó vazio; o
  administrador envia o formulário, registra POST/chave/202 e acompanha até ready.
  Reutilizar a retirada já comprovada ou retirar esse mesmo quarto nó ao final.
- A spec administrativa cita falha do banco local identificada no painel. Storage
  e falha total do gerenciador já foram exercitados pela UI; a matriz SQL/backend/
  controle foi comprovada por API. Uma única simulação SQL pelo botão, seguida de
  restauração, comprova a seleção e tradução específicas desse cenário.
- A leitura de saúde desatualizada tem teste unitário e observação de erros reais
  do painel. Para evidência explícita do aviso de dados antigos, bloquear apenas
  GET do painel por mais de 15 segundos e capturar o indicador; não alterar o
  gerenciador e não confundir falha de consulta com falha observada dos componentes.

Esses três pontos cabem numa sessão administrativa curta, com provisionamento do
quarto nó preparado pelo coordenador. A tentativa de usuário comum na API, privacidade
do painel e retirada real já têm evidência e não precisam ser repetidas sem motivo.

A corrida publicação/admissão de 6.3/8.3 e a queda durante cópia de 5.6/8.3 pertencem
às frentes backend em execução. Não declarar conclusão delas usando somente a tela
Concluído. A inicialização completa pelo script continua sob 8.6. A medição de 2 GiB
continua sob 8.5; não bloquear a classificação de 8.4 apenas por esse tamanho, desde
que as limitações sejam registradas.

## Orçamento de disco para 2 GiB

Estimativa baseada no código e na prova local da versão MinIO utilizada, não numa
nova consulta ao ambiente. Cada escrita final explícita gera uma versão física;
essa versão é replicada para os três sites. Confirmar três sites pode criar três
versões completas, cada uma presente nos três sites. A limpeza atual remove partes
temporárias de operações terminais e não remove versões finais.

Para um arquivo F = 2 GiB, sem falhas nem versões finais extras:

| Componente | Estimativa no mesmo disco do host |
| --- | ---: |
| Uma versão de cada parte temporária, replicada a três sites | 3F = 6 GiB |
| Três escritas finais explícitas, cada versão replicada aos três sites | 9F = 18 GiB |
| Origem sparse de zeros | quase zero de blocos, 2 GiB de tamanho lógico |
| Download nativo e cópia feita por saveAs, se ambos forem preservados | até 2F = 4 GiB |
| Dados somados antes de contar limpeza, staging e margem | até 28 GiB |

Os 24 GiB citados antes cobriam partes e finais; não cobriam todas as cópias do
navegador no disco. Downloads devem ser feitos depois de encerrar a medição e,
preferencialmente, após verificar que a limpeza realmente terminou. Não contar
com espaço liberado por temporários antes da confirmação.

Para planejar uma execução sem apagar dados, reservar aproximadamente 36 GiB livres
no filesystem que contém os volumes e artefatos: 28 GiB acima e cerca de 8 GiB para
staging, SQL, logs e reserva operacional. Isso é uma margem de planejamento, não
um limite superior garantido. Uma nova versão final completa replicada adiciona
até 6 GiB; recuperação repetida pode acumular várias. Instabilidade de membership
ou versões crescendo exige interromper novas tentativas e investigar antes de
continuar. Cancelar upload não autoriza apagar versões publicadas.

Se artefatos e volumes estiverem em discos distintos, separar os orçamentos pelos
mountpoints reais; não somar espaço livre de discos que o MinIO não usa. Uma origem
não sparse acrescenta 2 GiB. Guardar apenas o download nativo, sem duplicá-lo em
saveAs, pode reduzir até 2 GiB fora da medição, se o mecanismo do CLI for confirmado.
Não pressupor essa redução nem deduplicação de bytes entre versões.

Os cerca de 22 GiB informados após o reset não bastam para essa configuração de
2 GiB. Uma execução exige mais disco, realocação autorizada dos volumes/artefatos ou
mudança de estratégia de armazenamento que teria seu próprio escopo e validação.
Enquanto isso, registrar 8.5 pendente, preservando a medição real de 256 MiB.
