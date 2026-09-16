## MODIFIED Requirements

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

## ADDED Requirements

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
