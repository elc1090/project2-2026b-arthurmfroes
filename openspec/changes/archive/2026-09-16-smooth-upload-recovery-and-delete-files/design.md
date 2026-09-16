## Context

Ver proposal.md. A fila em `frontend/src/queue.ts` substitui `row.progress` pelo total de partes que a API ainda classifica como disponíveis. Quando a única cópia temporária de uma parte se perde, esse total diminui e o reenvio percorre visualmente o mesmo intervalo. No backend, arquivos publicados ficam em `files`, as versões físicas ficam em `object_copies` e os planos de admissão selecionam todos os arquivos dessa tabela. Não existe estado persistido de exclusão.

## Goals / Non-Goals

**Goals:**

- Separar o maior progresso já apresentado da disponibilidade autoritativa usada para decidir reenvios.
- Confirmar a exclusão lógica numa única transação e limpar versões físicas sem depender da disponibilidade simultânea dos sites.
- Manter a barreira de `publication_generation` entre exclusões e admissão de nós.

**Non-Goals:**

- Lixeira, restauração, retenção temporária ou exclusão de pastas.
- Cancelar um download que já obteve autorização e abriu sua versão física.
- Alterar a política de replicação ou adicionar dependências.

## Decisions

### 1. Progresso visual monotônico com estado explícito de recuperação

A transferência manterá um maior percentual apresentado durante a sessão da página. As respostas da API continuam substituindo o estado autoritativo das partes, mas só podem elevar a barra. Quando o total recuperável ficar abaixo desse maior valor e houver arquivo local para reenviar partes faltantes, a linha mostrará recuperação após falha de nó. O reenvio continua usando somente `availability`, nunca o percentual visual.

Isso preserva a correção do protocolo e impede a oscilação. Manter a barra igual ao total recuperável foi rejeitado porque causa o comportamento relatado. Somar todo byte retransmitido foi rejeitado porque poderia ultrapassar 100%.

### 2. Tombstone separado do catálogo ativo

Uma migração criará uma tabela de exclusões permanentes com `file_id`, `operation_id`, `owner_id` e horário. A transação de exclusão bloqueará o arquivo, verificará o proprietário, gravará o tombstone, removerá a linha de `files` e incrementará `cluster_configuration.publication_generation`. Repetir a exclusão com o mesmo proprietário e identificador encontrará o tombstone e retornará sucesso.

Remover a linha de `files` libera imediatamente os índices únicos de nome e exclui a operação dos planos atuais de recuperação. O tombstone preserva a intenção para idempotência, limpeza e filtragem de `GET /uploads`. Alterar `upload_operations.status` foi rejeitado porque ampliaria os estados e as restrições de uma operação cujo upload já terminou.

### 3. Limpeza por recibo físico

Um worker elegível percorrerá tombstones que ainda possuem `object_copies`. Para cada recibo, abrirá o storage do node registrado, enumerará somente as versões cuja chave seja exatamente o `object_key` canônico e removerá cada `versionId` encontrado. Isso inclui versões órfãs deixadas por uma escrita que terminou antes de o recibo SQL ser persistido. Depois do sucesso, apagará o recibo correspondente numa transação que reconfirma o tombstone e a geração do storage. Falha de rede mantém o recibo para nova tentativa.

O plano de admissão ignora tombstones porque consulta somente `files`, mas a promoção também verificará que não restam recibos pendentes de exclusão para a geração do node. Assim um node que retorna não carrega uma versão excluída para o estado `ready`.

Apagar somente a linha do catálogo foi rejeitado porque deixaria objetos sem coleta. Exigir todos os storages antes de responder foi rejeitado porque uma única falha impediria a exclusão do ponto de vista do usuário.

### 4. Concorrência com download

O download novo continuará iniciando por uma transação que encontra a linha ativa em `files`. Depois que a exclusão remove essa linha, novas solicitações recebem não encontrado. Um fluxo que já abriu a versão física conserva seu leitor e pode terminar; tentar interrompê-lo não recuperaria bytes já entregues e acoplaria a limpeza ao tempo da conexão.

### 5. API e interface

A API adicionará `DELETE /api/files/{id}`, autorizada pelo proprietário e idempotente para um tombstone dele. A listagem mostrará uma ação de excluir com confirmação explícita. Após sucesso, o frontend atualiza a pasta e remove qualquer transferência concluída ligada ao arquivo. A restauração da fila omitirá operações com tombstone.

## Risks / Trade-offs

- [Um storage pode ficar fora do ar por muito tempo] -> O tombstone e o recibo permanecem persistidos, e a readmissão da geração afetada espera a limpeza.
- [A barra pode ficar em 100% durante um reenvio] -> O estado textual de recuperação continua visível e `Concluído` permanece reservado à publicação.
- [A remoção física pode retornar resultado ambíguo] -> A repetição usa a versão exata e só retira o recibo depois de uma resposta de sucesso ou confirmação equivalente de ausência.
- [A listagem S3 usa correspondência por prefixo] -> A limpeza valida a igualdade da chave retornada e nunca remove outra chave que apenas compartilhe o prefixo.
- [Tombstones crescem com o uso] -> Esta change os conserva para idempotência e auditoria mínima; política de expiração fica fora do escopo.

## Migration Plan

1. Aplicar a migração do tombstone antes de expor a rota de exclusão.
2. Publicar backend e frontend compatíveis com a tabela nova.
3. Em rollback, retirar a rota e o worker. Manter a tabela e seus registros é seguro; arquivos já excluídos não devem ser recriados.
