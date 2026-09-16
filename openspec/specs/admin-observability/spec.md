## Purpose

Permitir observar e demonstrar o comportamento distribuído, oferecendo controles reais de infraestrutura sem transformar o painel em requisito de disponibilidade.

## Requirements

### Requirement: Visão didática do cluster
O painel SHALL apresentar identidade, estado, componentes, motivo e horário da última transição, configuração ativa e progresso de sincronização dos nós, distinguindo observação atual de informação desatualizada.

#### Scenario: Falha de componente
- **WHEN** o banco local do nó 2 falha
- **THEN** o painel mostra qual componente falhou e a retirada do nó inteiro feita pelo gerenciador

### Requirement: Administração real autorizada
O painel SHALL permitir a administradores registrar nós, acompanhar admissão, solicitar retirada permanente e consultar informações operacionais de uploads pendentes, usando as mesmas regras do gerenciador. Essa consulta SHALL mostrar identificador, estado, progresso, cópias, sites e erros operacionais, sem expor conteúdo, nomes ou caminhos privados, credenciais ou permitir retomar/cancelar uploads alheios. Usuários comuns SHALL não executar essas operações administrativas.

#### Scenario: Tentativa não administrativa
- **WHEN** um usuário comum tenta remover um nó pela API administrativa
- **THEN** a operação é negada e a composição do cluster permanece inalterada

#### Scenario: Diagnóstico de confirmação pendente
- **WHEN** o administrador consulta um upload de outro usuário que aguarda uma cópia
- **THEN** identifica o site pendente e o estado operacional, sem obter o arquivo, seu nome, seu caminho ou permissão de modificá-lo

### Requirement: Simulação de falhas no desenvolvimento
O painel SHALL permitir escolher qualquer nó cadastrado, inclusive o que executa o gerenciador ativo, provocar falha total ou parcial e recuperar a simulação, identificando explicitamente que a falha é simulada. Falhas parciais SHALL permitir selecionar backend, banco local, storage local ou comunicação de controle. Falha total SHALL tornar indisponíveis todas as operações de usuário daquele nó. A simulação SHALL produzir rejeição efetiva das operações correspondentes e acionar a detecção normal, sem alterar apenas o indicador do painel. Testes de processo morto e partição real SHALL complementar a demonstração.

#### Scenario: Falha didática do storage
- **WHEN** o administrador ativa a simulação de storage indisponível no nó 3
- **THEN** as operações locais de storage são rejeitadas e o gerenciador desqualifica o nó sem intervenção adicional

#### Scenario: Falha total de qualquer nó
- **WHEN** o administrador seleciona qualquer um dos nós e provoca falha total
- **THEN** esse nó deixa de atender usuários, sua ausência é detectada e os demais nós saudáveis continuam operando conforme o quorum disponível

#### Scenario: Falha do nó gerenciador
- **WHEN** a falha total atinge o nó que executa o gerenciador ativo
- **THEN** sua atividade de gerenciador também cessa e outra instância assume a detecção e a exclusão

#### Scenario: Recuperação pelo painel
- **WHEN** o administrador encerra a falha provocada no nó selecionado
- **THEN** o nó passa pelo fluxo de recuperação e sincronização antes de voltar ao tráfego, sem promoção forçada pelo painel

### Requirement: Independência do painel
As decisões automáticas SHALL continuar quando nenhum navegador estiver aberto ou a interface administrativa estiver indisponível.

#### Scenario: Painel fechado
- **WHEN** um nó falha com o painel fechado
- **THEN** o gerenciador exclui o nó e permite que uploads prossigam sob a nova configuração
