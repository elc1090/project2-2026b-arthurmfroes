## Purpose

Oferecer a experiência inicial do Drive com pastas e transferência de arquivos, expondo somente dados cuja publicação foi confirmada.

## Requirements

### Requirement: Hierarquia privada de pastas
O sistema SHALL permitir criar e listar pastas e subpastas do usuário, validando a existência e propriedade da pasta pai. Nomes iguais no mesmo diretório SHALL retornar conflito nesta primeira versão.

#### Scenario: Criação de subpasta
- **WHEN** o usuário cria uma pasta dentro de uma pasta própria existente
- **THEN** a subpasta aparece na listagem desse diretório em qualquer nó elegível

#### Scenario: Pai inválido
- **WHEN** a pasta pai não existe ou pertence a outro usuário
- **THEN** a criação é rejeitada sem inserir uma pasta órfã

### Requirement: Upload e download pela interface
O sistema SHALL oferecer interface para selecionar pasta, enviar múltiplos arquivos em uma única seleção, acompanhar cada operação e baixar arquivos publicados. O seletor e a API SHALL aceitar qualquer tipo de arquivo, inclusive extensão desconhecida, ausência de extensão e conteúdo vazio, sem exigir preview ou conversão. O download SHALL preservar os bytes e o nome do arquivo. Sucesso de upload SHALL obedecer à capacidade consistent-storage.

#### Scenario: Transferência confirmada
- **WHEN** um upload recebe sucesso e o usuário baixa o arquivo por outro nó elegível
- **THEN** o conteúdo baixado é idêntico ao enviado

#### Scenario: Seleção de tipos diferentes
- **WHEN** o usuário seleciona juntos uma imagem, um arquivo binário de extensão desconhecida e um arquivo vazio sem extensão
- **THEN** cada arquivo recebe uma transferência independente para a pasta escolhida, sem rejeição por tipo

### Requirement: Visibilidade consistente
O sistema SHALL listar apenas arquivos publicados, usando metadados compartilhados, e SHALL manter operações pendentes no acompanhamento privado do proprietário. A consulta administrativa SHALL limitar-se às informações operacionais definidas em admin-observability, sem conceder acesso ao conteúdo privado.

#### Scenario: Replicação incompleta
- **WHEN** um arquivo aguarda confirmação de uma cópia obrigatória
- **THEN** ele não aparece na listagem normal nem pode ser baixado pela API de arquivos

### Requirement: Arquivos grandes com memória limitada
O sistema SHALL transferir arquivos em partes automaticamente, sem exigir divisão manual ou ajuste de configuração por arquivo. Navegador e backend SHALL processar os bytes com memória limitada pelo tamanho das partes e concorrência, sem materializar o arquivo inteiro em memória. O sistema SHALL suportar o arquivo de referência de 2 GiB, sem tratar esse tamanho nem o antigo limite de 100 MiB como teto de arquivo. Limites de transporte SHALL aplicar-se às partes, incluindo overhead. Falta de capacidade ou limite técnico do armazenamento SHALL produzir erro explícito sem falso sucesso.

#### Scenario: Arquivo de referência
- **WHEN** o usuário envia um arquivo de 2 GiB com capacidade disponível nos sites obrigatórios
- **THEN** a transferência usa partes automaticamente, mantém memória limitada e pode concluir após confirmar as cópias

#### Scenario: Referência não é teto
- **WHEN** um arquivo excede 2 GiB, mas cabe na capacidade e nos limites técnicos do armazenamento
- **THEN** o protocolo permite seu envio em partes sem ajuste manual nem rejeição pelo tamanho de referência

### Requirement: Acompanhamento e erro por arquivo
A interface SHALL distinguir Enviando, Confirmando armazenamento e Concluído. Também SHALL mostrar espera por conexão, recuperação de partes após falha de nó, necessidade de selecionar o arquivo novamente, erro e cancelamento quando aplicáveis. Durante Enviando, o percentual apresentado SHALL ser monotônico dentro da sessão da página, mesmo quando o backend identificar que uma parte antes disponível precisa ser reenviada. O progresso dos bytes SHALL não ser apresentado como sucesso de publicação. Falha de uma transferência SHALL não cancelar as demais; cada arquivo SHALL oferecer repetição ou cancelamento conforme seu estado. Cancelar uma operação pendente SHALL impedir sua publicação se o cancelamento for confirmado antes dela; se a publicação já ocorreu, a resposta SHALL informar Concluído e não excluir o arquivo.

#### Scenario: Bytes enviados sem confirmação
- **WHEN** todos os bytes foram enviados mas ainda falta uma cópia obrigatória
- **THEN** a interface mostra Confirmando armazenamento, sem declarar Concluído ou oferecer download do pendente

#### Scenario: Erro isolado
- **WHEN** uma transferência falha enquanto outras estão na fila
- **THEN** as demais continuam e o arquivo com erro pode ser repetido sem duplicar sua operação

#### Scenario: Cancelamento concorrente com publicação
- **WHEN** o usuário cancela enquanto o backend tenta publicar
- **THEN** apenas um resultado é confirmado: cancelamento sem publicação ou conclusão já efetivada, visível ao consultar a operação

#### Scenario: Parte perdida durante o envio
- **WHEN** a queda de um nó torna indisponível uma parte já contabilizada e o arquivo original permanece selecionado
- **THEN** a interface informa a recuperação, reenvia somente as partes necessárias e não reduz o percentual já mostrado

### Requirement: Retomada com página aberta ou reaberta
O sistema SHALL persistir o estado das transferências no backend e disponibilizá-lo ao proprietário após login, independentemente de estado local do navegador. Com a página aberta, falhas transitórias de rede SHALL permitir nova tentativa automática com espera entre tentativas e consulta das partes ainda disponíveis. Após reabrir, a interface SHALL solicitar nova seleção quando precisar de bytes locais, aceitar seleção múltipla para associar arquivos às operações e validar identidade de conteúdo antes de reutilizar partes. Operações com bytes preservados suficientes SHALL continuar a confirmação no backend sem depender da página aberta.

#### Scenario: Reconexão com página aberta
- **WHEN** a conexão cai durante o envio e depois retorna
- **THEN** a interface consulta a operação e envia somente as partes faltantes, sem criar outro arquivo

#### Scenario: Reabertura com envio incompleto
- **WHEN** o usuário reabre a página e autentica com uploads incompletos
- **THEN** a interface recupera as operações, pede os arquivos necessários e retoma as partes faltantes após validar o conteúdo selecionado

#### Scenario: Reabertura durante confirmação
- **WHEN** a página é fechada depois que os bytes necessários ficaram preservados e reaberta após a publicação
- **THEN** a interface mostra Concluído para a mesma operação sem nova seleção ou upload

### Requirement: Interface com identidade própria
A interface SHALL usar o nome Acervo e identidade visual própria, com navegação por pastas e subpastas, caminho da pasta atual, listagem de arquivos e área de transferências. A área administrativa SHALL ser separada da navegação cotidiana de arquivos. O visual SHALL usar tipografia legível, espaçamento e cores consistentes, sem logotipo ou elementos de marca do Google Drive.

#### Scenario: Navegação cotidiana
- **WHEN** o usuário abre uma subpasta durante uma transferência
- **THEN** consegue identificar o caminho atual, listar seus arquivos e acompanhar a transferência sem entrar na área administrativa

### Requirement: Exclusão permanente de arquivo
O sistema SHALL permitir que o proprietário exclua permanentemente um arquivo publicado após confirmação explícita na interface. A confirmação SHALL retirar o arquivo da listagem, bloquear novos downloads, liberar seu nome no diretório e retirar sua operação do acompanhamento cotidiano. A exclusão SHALL ser idempotente e não SHALL oferecer restauração ou lixeira.

#### Scenario: Exclusão confirmada
- **WHEN** o proprietário confirma a exclusão de um arquivo publicado
- **THEN** o arquivo deixa de aparecer no diretório, novos downloads são rejeitados e outro arquivo pode usar o mesmo nome

#### Scenario: Repetição da exclusão
- **WHEN** o cliente repete a exclusão do mesmo arquivo após perder a resposta anterior
- **THEN** a API confirma o mesmo resultado sem restaurar o arquivo nem criar outra operação

#### Scenario: Exclusão sem autorização
- **WHEN** outro usuário tenta excluir o arquivo por seu identificador
- **THEN** a API não altera o arquivo nem expõe seus metadados
