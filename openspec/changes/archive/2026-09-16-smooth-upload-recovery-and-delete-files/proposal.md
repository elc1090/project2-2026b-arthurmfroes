## Why

Quando um nó perde a única cópia temporária de uma parte, a interface reduz o percentual e percorre o mesmo trecho durante o reenvio. Isso transmite instabilidade ao usuário mesmo quando a retomada automática funciona. O Acervo também permite publicar e baixar arquivos, mas ainda não oferece exclusão permanente.

## What Changes

- Manter monotônico o percentual exibido durante o envio, sem esconder do backend a perda real de partes.
- Informar na transferência quando o cliente estiver recuperando partes após uma falha de nó.
- Permitir que o proprietário exclua permanentemente um arquivo publicado após confirmação na interface.
- Remover o arquivo do catálogo e bloquear novos downloads antes da limpeza física distribuída.
- Permitir que downloads iniciados antes da exclusão terminem.
- Limpar em segundo plano todas as versões físicas da chave canônica do arquivo em cada site registrado, inclusive versões órfãs criadas antes da persistência de um recibo; retomar a limpeza quando um storage indisponível voltar e impedir que a recuperação de um nó restaure o arquivo excluído.
- Liberar o nome no diretório após a exclusão lógica e retirar a operação excluída do acompanhamento cotidiano.

## Capabilities

### New Capabilities

Nenhuma.

### Modified Capabilities

- `drive-files`: tornar o progresso visual de envio monotônico e adicionar exclusão permanente de arquivos pela interface e API.
- `consistent-storage`: definir a exclusão lógica atômica, a limpeza física por site e a interação com downloads e readmissão de nós.

## Impact

Serão afetados o modelo SQL de arquivos e operações, o catálogo, o serviço e as rotas de upload/download, a limpeza de objetos versionados, o plano de recuperação de nós, os tipos e a fila do frontend, a listagem de arquivos e seus testes. A API ganhará `DELETE /api/files/{id}`. Nenhuma dependência nova é necessária.
