## Why

O painel atual registra uma falha cooperativa no banco compartilhado: os processos continuam vivos e passam a rejeitar operações porque conhecem a simulação. Isso não demonstra como o cluster reage à perda inesperada de um serviço em desenvolvimento nem numa implantação com serviços separados no Railway, e a visualização atual também não explica a retirada, o redirecionamento e a recuperação do nó.

## What Changes

- Substituir a simulação cooperativa baseada em `node_faults` por atuação real sobre a infraestrutura, sem gravar uma intenção que o gerenciador possa consumir antes de observar a queda.
- Introduzir um atuador administrativo separado do plano de controle do cluster, com congelamento de containers no Docker Compose e execução de sinais por SSH nos serviços Railway.
- Preservar `./scripts/dev.sh` como entrada completa do ambiente local, selecionando o adaptador Docker por configuração de ambiente e usando o adaptador SSH somente na implantação Railway.
- Permitir provocar e restaurar a indisponibilidade do backend, CockroachDB, MinIO ou nó lógico inteiro, preservando processos, volumes e identidade.
- Fazer o gerenciador, o balanceador e os clientes reagirem somente pelos mecanismos normais de timeout, lease, probes, exclusão, repetição e readmissão.
- Redesenhar o painel como um diagrama vivo da entrada, rotas, nós, componentes, gerenciador e relações de armazenamento, acompanhado por uma sequência didática do incidente observado.
- Rebaixar versão da configuração e geração de publicação a detalhes técnicos e manter o histórico bruto disponível para diagnóstico.
- Exibir separadamente a ação solicitada ao provedor e os fatos observados pelo cluster, sem declarar retirada ou recuperação antes da confirmação correspondente.
- Suprimir do access log do balanceador apenas leituras periódicas administrativas bem-sucedidas, preservando erros, mutações e demais requisições.

## Capabilities

### New Capabilities

- `infrastructure-fault-control`: provocar e restaurar falhas reais de serviços por meio de um atuador externo ao plano de controle, com contratos para Docker Compose e Railway.

### Modified Capabilities

- `admin-observability`: apresentar a arquitetura e a evolução dos incidentes de forma didática e operar o atuador real com autorização administrativa.
- `cluster-management`: garantir que uma falha provocada externamente siga exatamente a mesma detecção, exclusão, sucessão e readmissão de uma queda não planejada.
- `development-environment`: executar o mesmo contrato de falha real no Compose e manter os logs do balanceador legíveis durante a demonstração.

## Impact

Serão afetados o painel React, a projeção administrativa, as rotas administrativas, a configuração do Nginx, o Compose e a futura configuração dos serviços Railway. A mudança remove o caminho cooperativo de `node_faults` e adiciona um serviço atuador com mapeamento explícito entre nó, componente e instância de serviço. Em produção, cada imagem observada manterá um init mínimo como PID 1 e o workload num único grupo filho; o atuador abrirá uma sessão SSH no alvo cadastrado e invocará um helper fixo para enviar `SIGSTOP` ou `SIGCONT` a esse grupo. A chave privada ficará somente no atuador e nunca será enviada ao navegador ou aos backends do cluster. A implementação não integrará a API GraphQL do Railway.
