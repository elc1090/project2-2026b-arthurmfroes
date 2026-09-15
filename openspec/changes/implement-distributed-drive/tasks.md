## 1. Base executável e serviços locais

- [ ] 1.1 Criar backend/cmd/server com leitura de PORT, NODE_ID e endpoints; verificar configuração válida/inválida e go test ./..., sem adicionar dependências ainda não utilizadas.
- [ ] 1.2 Implementar acesso SQL e migrações iniciais de usuários, sessões, pastas, operações, partes, localizações de partes, arquivos, cópias e controle de cluster; verificar aplicação e reexecução das migrações e retry de transação serializável.
- [ ] 1.3 Apontar cada backend para seu CockroachDB e MinIO correspondentes; verificar Compose expandido, isolamento dos seis volumes e acesso SQL pelos três nós.
- [ ] 1.4 Corrigir bootstrap CockroachDB e MinIO para distinguir partida fria de reinício e validar peers completos; verificar duas inicializações com volumes preservados e reinício degradado sem bloqueio global.
- [ ] 1.5 Verificar na versão efetiva do MinIO a persistência local de PUT e leitura com peers isolados, incluindo multipart, checksums e proxy de HEAD; registrar versões e evidência num roteiro de integração antes da implementação de recibos.

## 2. Controle de nós e eleição

- [ ] 2.1 Implementar registro de nós, estados e configuração versionada no SQL; verificar identidade duplicada rejeitada e duas alterações concorrentes serializadas.
- [ ] 2.2 Implementar concessão temporária de gerenciador e mandato; verificar sucessão após queda e rejeição de escrita por mandato vencido.
- [ ] 2.3 Implementar canais internos autenticados, heartbeat e sondagens de backend, SQL e MinIO; verificar que credencial inválida não altera estado e que cada falha parcial produz evidência identificável.
- [ ] 2.4 Implementar exclusão automática do tráfego e confirmações obrigatórias em uma mesma configuração; verificar prazo configurado, ausência de aprovação manual e preservação dos volumes.
- [ ] 2.5 Implementar bloqueio de atendimento em nó não elegível ou sem autoridade atual; verificar requisições diretas, conexões persistentes e perda de quorum sem sucesso indevido.

## 3. Balanceamento reconciliado

- [ ] 3.1 Implementar reconciliador do Nginx com descoberta de controle por múltiplos endpoints; verificar geração, nginx -t e recarga após mudança de membership.
- [ ] 3.2 Tratar zero nós elegíveis e configuração indisponível; verificar resposta 503 e rejeição por backend mesmo quando um worker antigo ainda encaminha conexão.

## 4. Identidade e catálogo

- [ ] 4.1 Implementar cadastro por login único e senha, hash de senha, sessão compartilhada e logout; verificar cadastro sem e-mail, credenciais inválidas, concorrência e login/logout alternando entre os três backends.
- [ ] 4.2 Implementar autorização por proprietário e papel administrativo; verificar acesso cruzado a pasta, arquivo, listagem de transferências, consulta/envio de partes, retomada e cancelamento negado; verificar que novo login do proprietário recupera suas operações e que diagnóstico administrativo não revela conteúdo privado.
- [ ] 4.3 Implementar criação e listagem de pastas/subpastas; verificar pai inexistente, pai alheio, nomes duplicados e leitura consistente por diferentes nós.

## 5. Persistência e publicação de arquivos

- [ ] 5.1 Implementar operações pending, criação por chave de idempotência do usuário e manifesto imutável de partes/tamanho/SHA-256; verificar repetição igual, conflito de conteúdo/destino, resposta de criação perdida e tamanhos/offsets acima de 2 GiB sem teto associado ao teste.
- [ ] 5.2 Implementar envio idempotente de partes para objetos temporários locais e localização persistida por geração de storage; verificar hash/tamanho, parte duplicada/divergente, recebimento por nós diferentes e memória limitada no fluxo Go/S3.
- [ ] 5.3 Implementar consulta e reconciliação de partes recuperáveis; verificar cópia preservada, parte perdida, estado desconhecido por falta de autoridade e invalidação após substituição de volume sem descartar partes válidas.
- [ ] 5.4 Implementar montagem ordenada em fluxo, validação do checksum completo e escrita explícita local com recibo por site/versão lógica/geração conforme evidência de 1.5; verificar multipart, arquivo vazio, bytes fora de ordem/corrompidos, reinício da montagem e rejeição de confirmação baseada apenas em HEAD encaminhado.
- [ ] 5.5 Implementar coleta de todas as confirmações e publicação transacional condicionada à configuração vigente; verificar que pending não aparece no catálogo e que receber partes ou atingir 100% de envio não confirma o arquivo.
- [ ] 5.6 Implementar retomada por worker saudável com concessão/geração persistida e consulta de status; verificar queda do coordenador, rejeição de worker antigo, perda de resposta após commit e continuidade da confirmação com navegador fechado.
- [ ] 5.7 Revalidar operações quando nó é excluído ou admitido durante upload; verificar conclusão com todos os sobreviventes, espera por nó recém-admitido e impossibilidade de atender partes em nó desqualificado.
- [ ] 5.8 Implementar cancelamento idempotente e limpeza segura de temporários; verificar disputa com publicação, finalização atrasada, preservação de partes de operações ativas e versões publicadas, e ausência de expiração por fechamento de página.
- [ ] 5.9 Implementar download em fluxo por versão publicada e listagem privada; verificar igualdade SHA-256 pelos três nós, nome original, arquivo vazio, tipo desconhecido e indisponibilidade de pending.
- [ ] 5.10 Ajustar transporte Nginx/API para limites por parte com overhead, buffering e timeouts compatíveis; verificar transferência total acima do limite de uma requisição sem rejeição pelo tamanho do arquivo, rejeição de parte inválida e erros de capacidade sem falso sucesso.

## 6. Admissão e recuperação

- [ ] 6.1 Implementar associação de nó previamente provisionado com endpoints genéricos e estado joining; verificar entrada de quarto nó vazio sem reiniciar backends existentes.
- [ ] 6.2 Implementar sincronização e validação das versões publicadas em nó novo ou recuperado; verificar arquivos ausentes, checksum divergente e geração de volume alterada.
- [ ] 6.3 Implementar barreira transacional entre admissão e publicação; verificar upload concorrente com término da sincronização sem admitir nó com arquivo faltante.
- [ ] 6.4 Implementar retirada permanente administrativa com etapas de banco e MinIO, mantendo falha temporária separada; verificar que timeout não apaga volumes e que remoção informa impedimentos de quorum/topologia.

## 7. Interface e painel administrativo

- [ ] 7.1 Criar frontend React do Acervo e integração com entrada Nginx; verificar login/senha, pastas/subpastas, caminho atual, listagem, identidade visual própria, área administrativa separada e download nativo sem Blob do arquivo inteiro.
- [ ] 7.2 Implementar seleção múltipla sem filtro de tipo e fila por arquivo usando File/Blob e XMLHttpRequest, com duas partes de 32 MiB simultâneas no total como default; verificar concorrência limitada, progresso, arquivo vazio e continuidade dos demais após erro de um arquivo.
- [ ] 7.3 Criar visão administrativa de componentes, estados, motivo, configuração, cópias e eventos; verificar dados desatualizados identificados e resposta operacional sem nomes, caminhos, conteúdo ou credenciais de uploads alheios.
- [ ] 7.4 Implementar controles de registro, acompanhamento e remoção de nó; verificar autorização e uso das mesmas regras do gerenciador.
- [ ] 7.5 Implementar injeção e recuperação de falha total e parcial em qualquer nó escolhido, com seleção de backend, banco, storage ou comunicação; verificar rejeição efetiva das operações e detecção normal, sem editar apenas indicadores.
- [ ] 7.6 Cobrir pelo painel falha total do nó gerenciador e retorno do nó selecionado; verificar sucessão automática e recuperação sem promoção forçada para ready.
- [ ] 7.7 Restringir simulação ao desenvolvimento e manter canal autenticado para desfazê-la; verificar controles inacessíveis a usuários comuns e gerenciamento ativo com painel fechado.

- [ ] 7.8 Implementar preparação de manifesto e hash incremental em worker, com buffers limitados; verificar vetores conhecidos, partes finais menores, arquivo vazio e rejeição de arquivo reselecionado com mesmo nome/tamanho mas conteúdo diferente.
- [ ] 7.9 Implementar estados Enviando, Confirmando armazenamento e Concluído, além de preparação, espera, nova seleção, erro e cancelamento; verificar que bytes enviados não significam publicação e que cada linha permite ações compatíveis com seu estado.
- [ ] 7.10 Implementar retomada automática com página aberta e tentativas espaçadas; verificar perda de resposta de criação por chave persistida, queda durante parte, expiração de sessão e consulta das partes antes de reenvio sem duplicação.
- [ ] 7.11 Implementar recuperação da fila pelo backend após reabertura e login, associação de seleção múltipla e validação do conteúdo; verificar retomada sem estado local prévio, operação ambígua, privacidade após troca de conta e confirmação concluída sem pedir arquivo novamente.

## 8. Integração distribuída e entrega

- [ ] 8.1 Criar suíte reproduzível com falha total de cada nó e falha isolada de cada componente; verificar prazos, retirada automática e upload/download pelos sobreviventes.
- [ ] 8.2 Exercitar partição real e perda de quorum SQL; verificar que lado isolado não atende com estado antigo nem confirma uploads.
- [ ] 8.3 Exercitar falha durante cópia, alteração de configuração, queda do gerenciador e recuperação com uploads concorrentes; verificar catálogo e SHA-256 por site, incluindo peers isolados.
- [ ] 8.4 Exercitar frontend e painel de ponta a ponta nos cenários das seis specs, incluindo seleção múltipla, erro/cancelamento isolados, queda de conexão, reabertura e falha do coordenador; verificar partes preservadas/perdidas, identidade divergente rejeitada e uma única publicação, distinguindo falhas injetadas, processo morto e rede interrompida.
- [ ] 8.5 Transferir arquivos de 256 MiB e 2 GiB pelo navegador, com parâmetros iguais de partes/concorrência; registrar picos de memória do navegador e backend, confirmar ausência de buffers do arquivo inteiro e verificar SHA-256 final por site; declarar pendente se recursos impedirem execução real.
- [ ] 8.6 Atualizar README e roteiro de demonstração com comandos, identidade Acervo, retomada, contratos de configuração, distinção entre limites por parte e referência de 2 GiB, e limites técnicos comprovados; verificar inicialização pelo scripts/dev.sh, go test ./..., validações do frontend e Docker Compose, sem declarar sucesso para passos não executados.

## Quadro de execução

Política: docs/development-workflow.md. A tabela acompanha as tarefas acima; não é uma
segunda fila. Atualizada pelo coordenador após delegação, integração ou bloqueio.

| Frente | Responsável | Tarefas | Branch/worktree | Estado e próxima ação |
| --- | --- | --- | --- | --- |
| Coordenação | Agente principal | Contratos, revisão e integração | main / raiz do projeto | Registrar planejamento em commit; preparar base comum |
| Base backend | A atribuir | 1.1; depois 1.2 | A criar a partir do commit de planejamento | Aguardar base comum e contrato de configuração |
| Infraestrutura | A atribuir | 1.3–1.5 | A criar a partir do commit de planejamento | Aguardar base comum; verificar ferramentas e serviços disponíveis |

As demais frentes aguardam as dependências descritas na seção 11 do design.
