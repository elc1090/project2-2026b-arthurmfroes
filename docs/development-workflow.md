# Coordenação do desenvolvimento

Este documento define como agentes planejam, implementam e integram trabalho neste
repositório. Os requisitos da aplicação ficam nas specs do OpenSpec; a divisão e o
estado de cada implementação ficam na change correspondente.

## Coordenador e executores

O agente principal mantém o contexto da change e coordena a execução. Antes de
delegar, lê os artefatos, identifica dependências e define os contratos compartilhados
necessários à entrega. Começar com dois executores; abrir outras frentes apenas
quando houver trabalho independente e capacidade de revisão.

Cada delegação informa:

- IDs das tarefas e resultado esperado.
- Branch, worktree e arquivos sob responsabilidade do executor.
- Contratos existentes, dependências e limites do escopo.
- Verificações exigidas e formato da entrega.

O executor lê esta política e os artefatos da change, usa a skill OpenSpec aplicável
e modifica apenas sua área. Quando precisar alterar um contrato, requisito ou arquivo
de outra frente, comunica a necessidade ao coordenador antes de escrever nessa área.

O coordenador concentra a revisão de contratos, migrações compartilhadas, arquivos
de dependências, configuração principal e atualização consolidada dos artefatos.
Pode delegar um desses arquivos explicitamente a um único executor por vez.

## Branches, worktrees e integração

Cada frente ativa usa uma branch e uma worktree exclusivas, criadas a partir da mesma
base revisada que contém as specs. O coordenador registra caminhos e base no quadro
de execução do tasks.md. Worktree é isolamento de arquivos, não de containers: testes
simultâneos precisam de nomes de projeto Compose, portas e volumes distintos.

Integrar entregas pequenas após revisar o diff e suas verificações. Quando commits
locais estiverem autorizados, usar commits descritivos por entrega. Caso a autorização
se restrinja a um commit anterior, integrar os arquivos revisados e manter as mudanças
de implementação no working tree. Commit, push e deploy respeitam a autorização da
sessão; esta política não concede autorização permanente para essas ações.

O executor entrega os arquivos alterados, decisões tomadas, comandos executados com
resultados e verificações pendentes. Pode marcar tarefas concluídas em sua cópia após
cumprir todos os critérios. O coordenador consolida esses marcadores somente depois
de integrar e verificar a compatibilidade com as outras frentes. Uma tarefa parcialmente
implementada ou com verificação obrigatória pendente continua aberta.

Um teste com respostas simuladas valida o contrato local; integração distribuída exige
os serviços reais. A entrega registra essa distinção. Builds e ambientes de teste
devem atender à verificação necessária, sem iniciar um servidor de desenvolvimento
para o usuário ou interferir no ambiente que ele já executa.

## Retomada e revisão de requisitos

Ao assumir a coordenação em outro chat:

1. Ler esta política, openspec/config.yaml e os artefatos da change ativa.
2. Consultar git status, branches e worktrees; conferir o quadro de execução e se os
   executores registrados ainda estão ativos antes de redistribuir arquivos.
3. Comparar entregas e evidências com os marcadores de tasks.md. Registrar divergências
   e retomar a primeira dependência não atendida, preservando mudanças existentes.
4. Atualizar o quadro após delegação, integração ou bloqueio, com a próxima ação concreta.

Requisitos novos ou incompatibilidades de design passam por openspec-update-change.
O coordenador apresenta a revisão ao usuário e mantém proposta, specs, design e
tarefas coerentes. O quadro de execução registra responsáveis e evidências, sem
criar uma segunda fila de tarefas ou copiar os requisitos.

Encerrar uma entrega exige informar progresso consolidado, verificações executadas,
pendências e localização das mudanças. Arquivar a change somente depois de concluir
os critérios de implementação e receber o pedido correspondente.
