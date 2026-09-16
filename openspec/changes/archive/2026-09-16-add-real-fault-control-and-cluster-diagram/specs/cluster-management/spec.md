## MODIFIED Requirements

### Requirement: Retirada automática por falha total ou parcial
O gerenciador SHALL combinar verificações externas e informações locais de saúde. Falha do backend, do acesso ao banco local, do storage local ou expiração da comunicação de controle SHALL desqualificar o nó inteiro. Dentro do prazo configurado de detecção e propagação, ele SHALL ser excluído do tráfego de usuário e do conjunto obrigatório para uploads, sem aprovação manual. A decisão SHALL depender somente das observações e da autoridade compartilhada do cluster; uma ferramenta que provoque a queda SHALL não registrar intenção de exclusão nem fornecer ao gerenciador um caminho antecipado. Endpoints internos de saúde e recuperação SHALL continuar permitidos quando o componente ainda conseguir atendê-los.

#### Scenario: Falha parcial
- **WHEN** apenas o MinIO do nó 2 deixa de atender sem publicar uma intenção de falha
- **THEN** o gerenciador descobre a indisponibilidade por sondagem, desqualifica o nó 2 inteiro, registra o motivo e mantém os nós 1 e 3 elegíveis se saudáveis

#### Scenario: Falha integral
- **WHEN** todos os componentes do nó 2 ficam inacessíveis sem heartbeat de despedida
- **THEN** a expiração das verificações produz a exclusão automática e a atualização das rotas

### Requirement: Continuidade do gerenciamento
A queda da instância que executa o gerenciador SHALL permitir que outra instância saudável assuma, sem publicar versões conflitantes. A instância que cai SHALL não liberar antecipadamente a concessão em resposta a uma ação do painel; a sucessão SHALL respeitar a expiração e a aquisição normal da autoridade.

#### Scenario: Gerenciador cai
- **WHEN** o serviço que contém o gerenciador ativo para sem alterar a concessão compartilhada
- **THEN** outro gerenciador assume após a autoridade anterior expirar e conclui as decisões pendentes sem intervenção no painel

