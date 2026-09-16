## Purpose

Permitir observar e demonstrar o comportamento distribuído, oferecendo controles reais de infraestrutura sem transformar o painel em requisito de disponibilidade.

## Requirements

### Requirement: Visão didática do cluster
O painel SHALL apresentar um diagrama vivo com a entrada balanceada, as rotas atualmente elegíveis e cada nó lógico composto por backend, banco local, storage local e comunicação de controle. O diagrama SHALL identificar o gerenciador, estado, componente afetado, motivo da última transição e progresso de sincronização, distinguindo observação atual de informação desatualizada. Versão da configuração, geração de publicação, mandato e horários completos SHALL permanecer disponíveis como detalhes técnicos, sem ocupar o resumo principal.

#### Scenario: Falha de componente
- **WHEN** o banco local do nó 2 falha
- **THEN** o diagrama marca esse componente, mostra o nó inteiro fora da rota após a decisão do gerenciador e mantém as rotas dos sobreviventes saudáveis

#### Scenario: Observação desatualizada
- **WHEN** o painel não recebe uma observação recente de um nó
- **THEN** o nó aparece com informação desatualizada e não como saudável ou definitivamente indisponível

### Requirement: Administração real autorizada
O painel SHALL permitir a administradores registrar nós, acompanhar admissão, solicitar retirada permanente, consultar informações operacionais de uploads pendentes e solicitar falhas reais ao atuador de infraestrutura, usando autorização explícita para cada operação. A consulta de uploads SHALL mostrar identificador, estado, progresso, cópias, sites e erros operacionais, sem expor conteúdo, nomes ou caminhos privados, credenciais ou permitir retomar ou cancelar uploads alheios. Usuários comuns SHALL não executar essas operações administrativas.

#### Scenario: Tentativa não administrativa
- **WHEN** um usuário comum tenta remover um nó ou provocar uma falha pela API administrativa
- **THEN** a operação é negada e nenhum recurso de infraestrutura nem a composição do cluster é alterado

#### Scenario: Diagnóstico de confirmação pendente
- **WHEN** o administrador consulta um upload de outro usuário que aguarda uma cópia
- **THEN** identifica o site pendente e o estado operacional, sem obter o arquivo, seu nome, seu caminho ou permissão de modificá-lo

### Requirement: Independência do painel
As decisões automáticas SHALL continuar quando nenhum navegador estiver aberto ou a interface administrativa estiver indisponível. Depois que o atuador aceita uma ação, fechar o painel SHALL não cancelar a ação nem participar da detecção do cluster.

#### Scenario: Painel fechado
- **WHEN** um nó falha com o painel fechado
- **THEN** o gerenciador exclui o nó e permite que uploads prossigam sob a nova configuração

#### Scenario: Painel fechado após solicitação
- **WHEN** o administrador fecha o painel depois da confirmação de uma ação do atuador
- **THEN** o provedor conclui a ação e o cluster reage sem novas requisições do navegador

### Requirement: Sequência observada do incidente
O painel SHALL explicar um incidente como uma sequência que separa a ação aceita pelo provedor dos fatos observados pelo cluster: componente indisponível, falhas de sondagem, exclusão do nó, atualização das rotas, restauração da infraestrutura, sincronização e readmissão. Uma etapa SHALL aparecer concluída somente quando sua fonte autoritativa a confirmar.

#### Scenario: Falha ainda não detectada
- **WHEN** o provedor confirma a parada mas o gerenciador ainda não atingiu o limiar de falhas
- **THEN** o painel mostra a infraestrutura parada e mantém a detecção e a retirada como etapas pendentes

#### Scenario: Recuperação concluída
- **WHEN** o serviço volta, sincroniza as publicações e é readmitido
- **THEN** a sequência mostra separadamente restauração, sincronização, readmissão e retorno da rota

### Requirement: Histórico técnico acessível
O painel SHALL manter os eventos técnicos recentes acessíveis para diagnóstico sem misturá-los à explicação principal do incidente. Detalhes provenientes de eventos SHALL ser limitados a campos conhecidos e sanitizados.

#### Scenario: Inspeção de evento
- **WHEN** o administrador abre o histórico de uma exclusão
- **THEN** vê nó, tipo, versão, mandato, horário e motivo conhecido sem receber JSON arbitrário ou segredos
