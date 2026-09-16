## Why

O commit inicial contém a topologia de desenvolvimento, mas ainda não possui aplicação executável nem garante retirada de nós com falha e publicação consistente de arquivos. Precisamos implementar o Drive distribuído com nós simétricos e administração automática, usando as decisões desta conversa como critérios de aceitação.

## What Changes

- Implementar o Acervo com backend Go e interface React para cadastro por login e senha, pastas, subpastas, upload e download privados por usuário, com identidade visual própria e área administrativa separada.
- Permitir seleção de múltiplos arquivos de qualquer tipo, transferência em partes com memória limitada e retomada com a página aberta ou após reabertura e nova seleção dos arquivos. Cada transferência terá progresso, erro, repetição e cancelamento independentes.
- Tratar cada nó lógico como backend, CockroachDB local e MinIO local, com identidade e endpoints configuráveis.
- Implementar gerenciador com detecção de falhas totais e parciais, exclusão automática do tráfego e do conjunto obrigatório de armazenamento, e recuperação antes da readmissão.
- Publicar arquivos somente após confirmar conteúdo idêntico em todos os sites obrigatórios da configuração vigente, com operações idempotentes e proteção contra mudanças concorrentes de configuração.
- Adicionar painel didático que observa o gerenciador e executa operações reais de administração e simulação de falhas.
- Completar e verificar o ambiente de desenvolvimento existente. Deploy em produção fica fora desta change.

## Capabilities

### New Capabilities

- `user-access`: identidade e acesso privado consistente entre backends.
- `drive-files`: hierarquia de pastas, interface do Acervo, fila de arquivos de qualquer tipo e transferências grandes retomáveis pela interface e API.
- `cluster-management`: associação, saúde, exclusão automática, configuração versionada e recuperação dos nós.
- `consistent-storage`: partes recuperáveis, integridade na retomada, publicação, confirmação de cópias e idempotência dos arquivos.
- `admin-observability`: painel administrativo, eventos e falhas didáticas.
- `development-environment`: inicialização reproduzível e validação da topologia distribuída local.

### Modified Capabilities

Nenhuma. `openspec/specs/` ainda não contém capacidades.

## Impact

Backend em `backend/cmd/server` e pacotes necessários, migrações SQL, frontend React, configuração Nginx, scripts CockroachDB/MinIO e `docker-compose.dev.yml`. Dependências serão adicionadas quando suas etapas forem implementadas; o módulo atual não possui pacotes externos. O gerenciador será uma responsabilidade da aplicação, com coordenação persistida no CockroachDB, sem depender do painel para funcionar.

Criptografia opcional de arquivos, compartilhamento entre usuários, edição colaborativa, autoscaling de máquinas, implantação Railway e fusão de clusters independentes ficam fora desta primeira implementação. O antigo limite de 100 MiB por arquivo é substituído por envio automático em partes, com 2 GiB como tamanho de referência dos testes, não como teto. O usuário não precisa dividir arquivos nem ajustar configuração por tamanho. Limites de transporte se aplicam às partes; capacidade disponível e limites técnicos do armazenamento continuam existindo. As tags atuais das imagens permanecem aceitas para desenvolvimento, sujeitas à verificação de compatibilidade.
