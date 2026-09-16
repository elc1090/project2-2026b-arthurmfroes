## Quadro de execução

| Frente | Branch | Worktree | Responsabilidade | Estado |
| --- | --- | --- | --- | --- |
| Coordenação | `main` | raiz do projeto | integração, provas 4.1–4.3 e documentação | concluído |
| Catálogo/API | `implementation/permanent-file-delete` | `/tmp/acervo-permanent-file-delete` | tarefas 1.1–1.3, incluindo migração compartilhada | integrado em `5157418`–`dd9fae2` |
| Limpeza física | `implementation/deletion-cleanup-worker` | `/tmp/acervo-deletion-cleanup-worker` | tarefas 2.1–2.4, após contrato da migração | integrado em `23a6b82`, `47da2f2` e `26689b0`; prova distribuída concluída |
| Frontend | `implementation/smooth-upload-delete-ui` | `/tmp/acervo-smooth-upload-delete-ui` | tarefas 3.1–3.3, somente `frontend/` | integrado em `224a351` e `607cb6e` |

Verificações consolidadas em 16/09/2026: `go test -race -count=1 ./...`
passou em todos os pacotes; `npm test -- --run` passou 48 testes; `npm run build`
passou com TypeScript e Vite; `openspec validate smooth-upload-recovery-and-delete-files
--strict` passou. A frente de catálogo executou ainda CockroachDB real sobre banco
vazio e banco com arquivo publicado, e a frente de limpeza executou CockroachDB e
três MinIO reais para exclusão parcial, retry e download já aberto. Os containers
temporários foram removidos. A suíte SQL completa encontrou uma falha preexistente
no teste de duração de lock (liberação em 1,24 s para uma expectativa de 4–9 s); os
subtestes desta migração passaram isoladamente.

A prova real no `docker-compose.dev.yml` registrou barra constante em 98% durante
`Recuperando partes após falha de um node`, reenvio somente da parte perdida,
download novo em 404, download já aberto com 135.266.304 bytes, reutilização do nome
e a sequência `unavailable/2`, `syncing/1`, `syncing/0`, `ready/0`. Uma fixture com
três versões registradas e uma órfã terminou com zero versões da chave nos três
MinIO. O relatório está em
`frontend/verification/browser-smooth-upload-delete-result.md`.

## 1. Contrato persistido e exclusão lógica

- [x] 1.1 [Coordenador, contrato compartilhado] Adicionar a migração de tombstones com identidade do arquivo, operação, proprietário e horário; verificar aplicação sobre banco vazio e banco com arquivos publicados, incluindo as chaves e índices necessários para idempotência.
- [x] 1.2 Implementar a transação de exclusão no catálogo, com autorização, repetição idempotente, remoção de `files` e incremento de `publication_generation`; verificar com testes SQL os casos de proprietário, outro usuário, resposta perdida e reutilização imediata do nome.
- [x] 1.3 Expor `DELETE /api/files/{id}` e omitir operações tombstonadas das consultas do proprietário e do administrador; verificar códigos HTTP, ausência de metadados alheios e repetição da requisição nos testes da API.

## 2. Limpeza física e recuperação distribuída

- [x] 2.1 [Depende de 1.1] Adicionar ao storage a enumeração e remoção idempotente de todas as versões de uma chave final canônica, com igualdade exata de chave e `versionId` obrigatório, sem aceitar prefixos temporários; verificar que versões órfãs da mesma chave são removidas e outra chave com prefixo comum é preservada.
- [x] 2.2 [Depende de 1.2 e 2.1] Implementar o worker que percorre tombstones, limpa todas as versões da chave no site registrado e conserva recibos após falha ambígua; verificar sucesso parcial, versão órfã, nova tentativa e geração de storage substituída nos testes de upload.
- [x] 2.3 [Depende de 2.2] Impedir a admissão de um node enquanto sua geração tiver recibos físicos pendentes de exclusão e manter tombstones fora do plano de arquivos ativos; verificar queda durante exclusão, retorno do node, limpeza e readmissão em teste distribuído do backend.
- [x] 2.4 [Depende de 1.2 e 2.2] Verificar a concorrência entre exclusão e download: uma requisição nova após o commit recebe não encontrado, enquanto um fluxo que abriu a versão antes do commit termina com checksum correto.

## 3. Progresso e exclusão na interface

- [x] 3.1 Manter o maior progresso visual por transferência sem alterar o estado autoritativo das partes; verificar em `queue.test.ts` que a sequência disponível, perdida e reenviada nunca reduz a barra nem envia partes marcadas como disponíveis.
- [x] 3.2 Expor o estado “Recuperando partes após falha de um node” enquanto o cliente refaz partes abaixo do maior progresso apresentado; verificar transições para Enviando e Confirmando sem declarar Concluído antes da publicação.
- [x] 3.3 [Depende de 1.3] Adicionar a ação de exclusão com confirmação na listagem, atualizar o diretório após sucesso e retirar a transferência concluída correspondente; verificar cancelamento do diálogo, sucesso, erro e ausência da operação após recarregar a fila.

## 4. Integração e documentação

- [x] 4.1 [Coordenador, depende de 2.3, 2.4 e 3.3] Executar testes Go dos pacotes alterados, testes do frontend e validação do TypeScript, registrando os comandos e resultados no quadro de execução desta change.
- [x] 4.2 Verificar pelo navegador um upload com perda de node no meio do envio, confirmando barra monotônica e mensagem de recuperação; depois excluir o arquivo com um storage indisponível, confirmar bloqueio de novo download, conclusão do download já iniciado, reutilização do nome, limpeza após retorno e readmissão do node.
- [x] 4.3 Atualizar README e documentação operacional somente com os comandos e comportamentos entregues; verificar que a documentação distingue exclusão lógica confirmada de limpeza física pendente.
