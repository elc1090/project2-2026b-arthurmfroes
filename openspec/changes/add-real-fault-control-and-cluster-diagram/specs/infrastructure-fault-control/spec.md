## Purpose

Permitir demonstrações controladas de quedas reais sem informar previamente o plano de controle, usando o mesmo contrato administrativo em ambientes locais e hospedados.

## ADDED Requirements

### Requirement: Atuação externa ao cluster observado
O sistema SHALL executar a provocação e a restauração de falhas por um atuador de infraestrutura que não participa da eleição, da sondagem, da composição elegível nem da recuperação do cluster. Uma solicitação de falha SHALL atuar sobre o recurso selecionado sem gravar no estado compartilhado qualquer sinal que permita ao gerenciador antecipar a indisponibilidade.

#### Scenario: Queda sem aviso ao gerenciador
- **WHEN** um administrador provoca a falha do backend de um nó pronto
- **THEN** o backend deixa de responder e o gerenciador só descobre a queda pelas sondagens e pelos timeouts normais

#### Scenario: Painel fechado depois da ação
- **WHEN** o navegador fecha depois que o atuador aceitou uma falha
- **THEN** a infraestrutura conclui a ação e o cluster continua detectando e tratando a indisponibilidade sem depender do painel

### Requirement: Componentes e nó lógico completos
O atuador SHALL permitir interromper separadamente o backend, o banco local ou o storage local de um nó. A falha do nó inteiro SHALL interromper os três serviços do nó. Uma falha parcial SHALL preservar os demais componentes para que o gerenciador demonstre a desqualificação do nó lógico inteiro. O painel SHALL não oferecer uma partição isolada da comunicação de controle quando o ambiente não puder aplicá-la externamente sem modificar o processo observado.

#### Scenario: Storage parado
- **WHEN** o administrador provoca a falha do storage do nó 3
- **THEN** o processo ou serviço de storage do nó 3 fica indisponível enquanto backend e banco permanecem executando

#### Scenario: Nó inteiro parado
- **WHEN** o administrador provoca a falha total do nó 2
- **THEN** backend, banco e storage desse nó deixam de atender sem executar retirada administrativa ou apagar seus dados persistentes

### Requirement: Adaptadores de infraestrutura configuráveis
O atuador SHALL resolver cada nó e componente para alvos configurados no servidor, sem inferir destinos ou comandos a partir de dados fornecidos pelo navegador. Um modo de ambiente explícito SHALL selecionar exatamente um adaptador: Docker para a execução local ou SSH para a implantação Railway. O ambiente local SHALL congelar e retomar containers Docker Compose. A implantação hospedada SHALL abrir SSH somente para instâncias Railway previamente cadastradas e enviar sinais fixos ao processo principal. Modo desconhecido, configuração incompatível, falha de conexão ou mapeamento ausente SHALL falhar fechado sem alterar a composição do cluster e sem recorrer à antiga simulação cooperativa.

#### Scenario: Serviço Railway congelado
- **WHEN** o nó está mapeado para serviços Railway e o administrador confirma uma falha
- **THEN** o atuador acessa por SSH a instância cadastrada, envia `SIGSTOP` ao PID 1 e registra o resultado sanitizado do comando

#### Scenario: Comando fornecido pelo cliente
- **WHEN** uma solicitação inclui um hostname, identificador de instância ou comando arbitrário
- **THEN** o atuador ignora esses valores como destinos executáveis e resolve somente o alvo cadastrado no servidor

#### Scenario: Destino não cadastrado
- **WHEN** não existe mapeamento autorizado para o componente solicitado
- **THEN** o atuador rejeita a ação e nenhum outro serviço é afetado

#### Scenario: Ambiente configurado incorretamente
- **WHEN** o atuador inicia em modo local sem acesso ao Docker ou em modo Railway sem chave e alvos SSH válidos
- **THEN** os controles de falha ficam indisponíveis com diagnóstico explícito e nenhuma simulação alternativa é ativada

### Requirement: Restauração preserva identidade e dados
Restaurar uma falha SHALL retomar o mesmo processo interrompido com sua memória, volumes e configuração persistentes. O atuador SHALL enviar `SIGCONT` ao PID 1 no Railway e descongelar o container correspondente no Compose. O atuador SHALL limitar-se a recuperar a infraestrutura; admissão, sincronização e retorno ao tráfego SHALL permanecer decisões automáticas do cluster.

#### Scenario: Restauração no Railway
- **WHEN** o administrador restaura uma instância anteriormente congelada
- **THEN** o atuador envia `SIGCONT` ao mesmo PID 1 e não força sua inclusão no conjunto elegível

#### Scenario: Retorno com publicações ausentes
- **WHEN** o serviço retorna depois de arquivos terem sido publicados nos sobreviventes
- **THEN** o nó permanece fora do tráfego até o fluxo normal de sincronização e readmissão terminar

### Requirement: Autorização e segredo do provedor
Somente uma conta administrativa autenticada SHALL solicitar ações ao atuador. O socket Docker, a chave SSH e os identificadores das instâncias SHALL permanecer no atuador, com o menor alcance disponível, e SHALL nunca ser devolvidos ao navegador, gravados em eventos do cluster ou distribuídos entre os backends observados.

#### Scenario: Usuário comum tenta parar serviço
- **WHEN** um usuário sem papel administrativo solicita uma falha
- **THEN** a ação é negada antes de qualquer chamada ao provedor

#### Scenario: Consulta do estado da ação
- **WHEN** o painel consulta uma ação aceita
- **THEN** recebe alvo, tipo, estado e erro sanitizado sem receber token, credencial ou payload privado do provedor
