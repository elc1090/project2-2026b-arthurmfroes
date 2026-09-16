## Purpose

Disponibilizar uma topologia local verificável para desenvolver e demonstrar replicação, retirada automática e recuperação de nós simétricos.

## ADDED Requirements

### Requirement: Topologia completa de desenvolvimento
O ambiente SHALL iniciar três nós lógicos com backend, CockroachDB e MinIO próprios, além de entrada balanceada e interface. Os três bancos SHALL formar um único cluster e os três storages SHALL possuir volumes independentes com replicação. Nenhum backend SHALL depender exclusivamente de cockroach-1 como acesso SQL.

#### Scenario: Inicialização completa
- **WHEN** o desenvolvedor executa o script de desenvolvimento após implementar a aplicação
- **THEN** a interface permite autenticar e transferir arquivos pelos três nós admitidos

### Requirement: Inicialização retomável e persistente
Os inicializadores SHALL tolerar reexecução com volumes existentes, verificar a configuração de replicação completa e não destruir dados. Inicialização fria pode exigir todos os sites previstos; reinício de nós já inicializados SHALL permitir operação degradada quando existir quorum e sites elegíveis.

#### Scenario: Reinício com um site indisponível
- **WHEN** o ambiente previamente inicializado reinicia com um MinIO parado e dois bancos com quorum
- **THEN** os nós saudáveis podem ser admitidos sem ficar bloqueados indefinidamente por um inicializador que exige todos os sites

#### Scenario: Configuração incompleta
- **WHEN** existe configuração de replicação mas falta um peer previsto
- **THEN** a inicialização identifica e reconcilia a diferença ou informa erro explícito sem declarar configuração completa

### Requirement: Validação demonstrável
O projeto SHALL fornecer comandos e verificações para falhas totais, parciais, partições, recuperação e confirmação de cópias, registrando resultados observados. Testes de sintaxe SHALL não ser apresentados como comprovação de tolerância a falhas.

#### Scenario: Teste distribuído
- **WHEN** a suíte de integração executa upload, falha de um nó e recuperação
- **THEN** verifica os bytes em cada site, a exclusão automática, a continuidade nos sobreviventes e a readmissão somente após sincronização

### Requirement: Verificação de arquivos grandes e retomada
O roteiro de desenvolvimento SHALL incluir transferência de referência de 2 GiB, seleção múltipla, erro por arquivo, queda de conexão com página aberta, reabertura do navegador e falha do nó coordenador. SHALL registrar integridade final e picos de memória do navegador e backend, comparando arquivos menores e o arquivo de referência com os mesmos parâmetros de partes e concorrência. Os 2 GiB SHALL ser referência de teste, não limite de aceitação. Verificações não executadas por falta de recursos SHALL ser declaradas como pendentes.

#### Scenario: Memória durante arquivo de referência
- **WHEN** o roteiro transfere arquivos de 256 MiB e 2 GiB com a mesma concorrência e tamanho de partes
- **THEN** registra picos de memória e verifica que os buffers de conteúdo permanecem limitados pelas partes, sem alocação proporcional ao arquivo completo, além de comparar os checksums finais

#### Scenario: Reabertura e falha de nó
- **WHEN** o navegador fecha durante um envio e o nó coordenador falha antes da retomada
- **THEN** após login e nova seleção o upload continua por nó elegível usando partes preservadas, reenvia as perdidas e publica uma única versão consistente
