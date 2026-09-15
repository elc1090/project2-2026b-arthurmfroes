## Purpose

Gerenciar nós lógicos simétricos com descoberta de falhas, exclusão automática e readmissão segura, sem depender da presença de um operador no painel.

## ADDED Requirements

### Requirement: Identidade e associação configuráveis
O sistema SHALL representar um nó lógico por backend, banco local e object storage local. Sua identidade e endpoints SHALL ser configuráveis sem alterar código ou exigir domínios de um provedor específico. Nós novos SHALL entrar sem receber tráfego até sua admissão.

#### Scenario: Novo endereço
- **WHEN** um nó vazio e alcançável é registrado com identidade e endpoints próprios
- **THEN** ele inicia associação e sincronização sem exigir alteração da lista em todos os backends existentes

### Requirement: Retirada automática por falha total ou parcial
O gerenciador SHALL combinar verificações externas e informações locais de saúde. Falha do backend, do acesso ao banco local, do storage local ou expiração da comunicação de controle SHALL desqualificar o nó inteiro. Dentro do prazo configurado de detecção e propagação, ele SHALL ser excluído do tráfego de usuário e do conjunto obrigatório para uploads, sem aprovação manual. Endpoints internos de saúde e recuperação SHALL continuar permitidos.

#### Scenario: Falha parcial
- **WHEN** apenas o MinIO do nó 2 deixa de atender
- **THEN** o gerenciador desqualifica o nó 2 inteiro, registra o motivo e mantém os nós 1 e 3 elegíveis se saudáveis

#### Scenario: Falha integral
- **WHEN** todos os componentes do nó 2 ficam inacessíveis
- **THEN** a expiração dos checks produz a mesma exclusão automática mesmo sem heartbeat de despedida

### Requirement: Decisão compartilhada e proteção contra isolamento
O sistema SHALL versionar e persistir as mudanças do conjunto elegível com autoridade única por versão. Um nó sem autoridade atual ou sem acesso consistente ao estado compartilhado SHALL rejeitar operações de usuário. A perda de quorum do banco SHALL impedir publicação e reconfiguração, sem formar clusters independentes.

#### Scenario: Partição de rede
- **WHEN** o nó 1 fica isolado e os nós 2 e 3 mantêm quorum
- **THEN** os nós 2 e 3 podem registrar sua configuração e o nó 1 não pode publicar ou servir usuários com uma autorização expirada

#### Scenario: Ausência de quorum
- **WHEN** nenhuma parte consegue confirmar transações do banco
- **THEN** a aplicação informa indisponibilidade e não confirma novos uploads

### Requirement: Recuperação antes da readmissão
O gerenciador SHALL manter nós retornando ou novos fora do tráfego e das confirmações obrigatórias até validar saúde e todas as versões publicadas sob uma configuração estável. A promoção SHALL ser automática após essa validação.

#### Scenario: Retorno com arquivos ausentes
- **WHEN** o nó 2 retorna após uploads concluídos durante sua ausência
- **THEN** ele recebe e verifica esses arquivos antes de voltar a atender e confirmar uploads novos

#### Scenario: Escrita durante sincronização
- **WHEN** um arquivo é publicado enquanto um nó está sincronizando
- **THEN** a admissão inclui esse arquivo ou repete a verificação antes da promoção

### Requirement: Falha temporária e remoção permanente distintas
O sistema SHALL preservar dados e identidade após uma falha temporária. Remoção permanente SHALL executar o procedimento de saída do banco e storage sem apagar volumes automaticamente por simples expiração de heartbeat.

#### Scenario: Queda breve
- **WHEN** um nó é desqualificado por timeout e retorna com seus volumes
- **THEN** ele segue recuperação sem reinicializar o banco nem destruir os objetos

### Requirement: Continuidade do gerenciamento
A queda da instância que executa o gerenciador SHALL permitir que outra instância saudável assuma, sem publicar versões conflitantes.

#### Scenario: Gerenciador cai
- **WHEN** o gerenciador ativo para durante uma falha de nó
- **THEN** outro gerenciador assume com nova autoridade e conclui a decisão sem intervenção no painel

