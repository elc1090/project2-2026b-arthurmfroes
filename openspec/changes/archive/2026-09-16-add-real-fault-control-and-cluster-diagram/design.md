## Context

O controle atual grava `node_faults` no mesmo CockroachDB que sustenta a autoridade do cluster. Cada backend consulta essa tabela e converte o valor em rejeições sintéticas. O gerenciador ainda percorre o ciclo de sondagem, mas o processo afetado continua vivo e conhece a ação. A interface apresenta cartões independentes e eventos genéricos, enquanto o Nginx registra as consultas administrativas feitas a cada cinco segundos.

O ambiente local usa Docker Compose. A implantação pretendida no Railway terá backend, CockroachDB e MinIO de cada nó como serviços separados. O Railway permite abrir uma sessão SSH dentro de uma instância e executar um comando, sem exigir que a imagem execute seu próprio servidor SSH. A reação distribuída existente usa sondagens a cada dois segundos, limiar de três falhas, concessão do gerenciador de quinze segundos e snapshots autoritativos consumidos pelo balanceador.

## Goals / Non-Goals

**Goals:**

- Fazer a demonstração provocar a mesma ausência observável que uma queda externa inesperada.
- Manter o algoritmo de detecção e recuperação independente de Docker, Railway e do painel.
- Preservar dados persistentes durante parada e restauração dos serviços.
- Explicar visualmente a relação entre entrada, rotas, nós, componentes, gerenciador e etapas do incidente.

**Non-Goals:**

- Transformar o atuador acadêmico em uma plataforma genérica de orquestração.
- Administrar projetos, variáveis, volumes ou escalabilidade do Railway pelo painel.
- Garantir disponibilidade quando a falha remove o quorum mínimo do CockroachDB ou todos os backends.
- Usar a interface administrativa como fonte de verdade para saúde ou associação.

## Decisions

### 1. Atuador separado do plano de controle

Um pequeno serviço de atuação receberá comandos internos autenticados de um backend que já validou a sessão administrativa. Ele terá o mapeamento imutável de `node_id + componente` para containers locais ou IDs de instância Railway e será o único processo com acesso ao socket Docker ou à chave SSH. O navegador e os backends do cluster não receberão essas credenciais.

O atuador não escreverá `node_faults`, `cluster_membership`, `manager_lease` ou `cluster_events` antes da queda. Seu registro de ações terá armazenamento separado do estado consumido pelo gerenciador. A projeção administrativa combinará o estado da ação com observações e eventos do cluster somente para apresentação.

Colocar a chave SSH em todos os backends foi rejeitado porque amplia a distribuição de um segredo capaz de executar comandos nos serviços. Fazer o gerenciador executar a interrupção foi rejeitado porque mistura a causa da falha com o mecanismo que deve descobri-la.

### 2. Mesmo contrato, adaptadores diferentes

O atuador exporá operações `stop`, `restore` e `status` sobre alvos tipados. O adaptador local usará pause/unpause do Docker para congelar todos os processos do container previsto no Compose. O adaptador Railway usará o cliente OpenSSH e IDs de instância configurados no servidor, sem integrar a API GraphQL ou instalar o Railway CLI.

Uma variável de modo com valores fechados `docker` e `railway-ssh` selecionará o adaptador na inicialização. O Compose usado por `./scripts/dev.sh` definirá `docker`, montará o socket do Docker somente no atuador e fornecerá o mapeamento dos serviços do projeto. A configuração Railway definirá `railway-ssh`, a chave dedicada, host keys e IDs de instância. O binário recusará modo ausente ou configuração cruzada; não haverá detecção implícita do ambiente nem fallback para `node_faults`.

No Railway, o PID 1 do namespace não aceita `SIGSTOP`, como comprovado na instância descartável. As imagens hospedadas executarão `tini` como PID 1 e manterão o workload como um único grupo filho. Um helper estático comum resolverá o único filho direto por `/proc/1/task/1/children`, validará a topologia e enviará `SIGSTOP` ou `SIGCONT` ao grupo inteiro. O atuador invocará somente os comandos fixos `/usr/local/bin/fault-signal stop`, `/usr/local/bin/fault-signal restore` e `/usr/local/bin/fault-signal status`; não assumirá PID 2 nem interpolará valores da requisição. Os sinais não podem ser tratados pelo workload: o primeiro interrompe sua execução sem shutdown e o segundo retoma os mesmos processos. Isso evita a Restart Policy, conserva memória e volumes e mantém a indisponibilidade até a restauração explícita. `SIGKILL` ficará restrito a uma verificação separada de crash e reinício automático, pois não permite que o botão restaure o mesmo processo.

O backend, o CockroachDB e o MinIO usarão Dockerfiles Railway com o mesmo init e helper. O Compose continuará usando o adaptador Docker e não dependerá desses PIDs. A chave pública dedicada será cadastrada uma vez na conta; a Railway CLI poderá ser usada nessa preparação, mas não existirá na imagem nem no caminho de execução do atuador.

Como a Railway não publica uma lista autoritativa e estável das chaves de `ssh.railway.com`, o bootstrap registrará num segredo `known_hosts` as chaves observadas numa sessão administrativa controlada. As operações seguintes usarão `StrictHostKeyChecking=yes` e falharão fechadas diante de chave desconhecida. Rotação exigirá atualização explícita do segredo; o atuador nunca usará `StrictHostKeyChecking=no` ou `accept-new`.

O atuador chamará `ssh` com argumentos fixos, sem construir uma linha de shell com conteúdo da requisição. Comandos fornecidos pelo navegador, descoberta por nome parcial e alteração remota da Restart Policy foram rejeitados. Todos os destinos serão cadastrados explicitamente e validados na inicialização.

### 3. Falha total é composição de falhas de infraestrutura

Backend, banco e storage serão alvos independentes. “Nó inteiro” criará uma operação composta que congela todos os alvos do nó; o backend será interrompido primeiro para cessar atendimento antes das demais ações. O painel continuará mostrando a saúde da comunicação de controle, mas não oferecerá uma falha seletiva desse canal: controle e API de usuário compartilham o processo backend, e o Railway não fornece ao container a capacidade de alterar a rede de outra instância.

Uma operação parcial conserva os demais serviços. Isso permite demonstrar que a perda de apenas MinIO ou CockroachDB desqualifica o nó lógico inteiro.

### 4. Ação do provedor e reação do cluster são estados distintos

O painel não inferirá que um nó foi excluído porque a parada foi aceita. A sequência didática combinará:

- estado do atuador: solicitada, executando, parada confirmada, restauração confirmada ou erro;
- observação do nó: componentes conhecidos, idade e motivo;
- eventos do cluster: exclusão, mudança da configuração, início da sincronização e readmissão;
- snapshot do balanceador: nós que recebem tráfego.

Os eventos administrativos ganharão uma projeção com campos conhecidos, incluindo mandato e detalhes sanitizados. O histórico bruto ficará recolhido abaixo do diagrama.

### 5. Diagrama em HTML e CSS

O diagrama será composto por elementos semânticos do React e conectores em CSS ou SVG controlado pelo componente. Ele mostrará load balancer no topo, três colunas de nós, componentes dentro de cada nó, badge do gerenciador e linhas de rota somente para membros prontos. Sincronização usará linha pontilhada e falha ou exclusão interromperá visualmente a conexão. Selecionar um nó abrirá seus detalhes, ações e dados técnicos sem sobrecarregar o desenho principal.

Uma biblioteca de grafos foi rejeitada porque a topologia é pequena e fixa o bastante para HTML responsivo, evitando dependência e interação desnecessárias.

### 6. Filtragem restrita dos access logs

O Nginx usará logging condicional baseado em método, caminho e status. Somente `GET` bem-sucedidos dos endpoints periódicos do painel serão omitidos. Respostas de erro para esses caminhos e qualquer mutação continuarão no access log.

Desligar o access log inteiro foi rejeitado porque esconderia falhas de proxy, uploads e ações administrativas.

## Risks / Trade-offs

- [A chave SSH pode permitir acesso a mais serviços que os alvos da demonstração] → Mantê-la somente no atuador, registrar uma chave dedicada e aceitar apenas IDs cadastrados; documentar o alcance efetivo antes da implantação.
- [O socket Docker concede controle amplo sobre o daemon local] → Montá-lo somente no atuador de desenvolvimento e restringir todas as operações a serviços com projeto e nomes cadastrados.
- [Congelar o próprio atuador impediria restaurações] → Não cadastrar o atuador como alvo e executá-lo fora dos nós observados.
- [O PID 1 ignora `SIGSTOP` e o gateway precisa aceitar uma nova sessão durante a falha] → Manter um init mínimo fora do grupo do workload e validar `SIGSTOP`, nova sessão e `SIGCONT` numa instância descartável antes de ligar os controles; não substituir silenciosamente por simulação cooperativa.
- [A Railway pode apresentar ou rotacionar host keys sem uma lista pública autoritativa] → Fazer bootstrap controlado do `known_hosts`, manter verificação estrita em runtime e falhar fechado até atualização explícita quando surgir chave desconhecida.
- [A sessão SSH pode cair depois de entregar o sinal e antes de confirmar a saída] → Consultar o estado do processo por nova sessão e apresentar resultado indeterminado até a confirmação.
- [Uma falha total composta pode terminar parcialmente] → Registrar cada alvo separadamente, exibir o resultado parcial e permitir restauração idempotente dos componentes já parados.
- [Parar CockroachDB ou MinIO pode perder quorum] → Exigir confirmação explícita, mostrar o impacto esperado e deixar o cluster falhar fechado quando não houver autoridade suficiente.
- [Um `SIGKILL` aciona a Restart Policy e impede restauração manual do mesmo processo] → Usar `SIGSTOP`/`SIGCONT` no fluxo do painel e reservar `SIGKILL` para a prova explícita de crash transitório.

## Migration Plan

1. Introduzir o serviço atuador com seleção explícita de modo e controles desabilitados com diagnóstico quando a configuração exigida estiver ausente.
2. Integrar o modo Docker ao Compose e verificar `./scripts/dev.sh` com falhas reais antes de retirar a simulação cooperativa.
3. Trocar o painel e a API para operações assíncronas do atuador; remover leituras e gravações de `node_faults` do caminho de execução.
4. Implantar o atuador Railway separado, cadastrar os IDs de instância, instalar somente o cliente OpenSSH e fornecer uma chave privada dedicada como segredo.
5. Verificar parada e restauração de um componente por vez; depois verificar um nó comum e o nó gerenciador durante upload.
6. Em rollback, desabilitar os controles do atuador e manter o painel somente para observação. Não restaurar a simulação compartilhada automaticamente.
