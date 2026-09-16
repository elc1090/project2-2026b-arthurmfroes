## MODIFIED Requirements

### Requirement: Validação demonstrável
O projeto SHALL fornecer controles e verificações para parar de fato processos ou serviços em falhas totais, parciais e partições, além de recuperar esses recursos e confirmar as cópias. O ambiente local SHALL exercitar o mesmo contrato administrativo usado por provedores hospedados, trocando somente o adaptador de infraestrutura. Cada rodada SHALL registrar resultados observados; marcações cooperativas, respostas simuladas e testes de sintaxe SHALL não ser apresentados como comprovação de tolerância a falhas.

#### Scenario: Teste distribuído
- **WHEN** a suíte de integração executa upload, para um serviço de nó sem informar o gerenciador e depois o restaura
- **THEN** verifica os bytes em cada site, a detecção automática, a continuidade nos sobreviventes e a readmissão somente após sincronização

#### Scenario: Falha do gerenciador local
- **WHEN** o Compose para abruptamente o backend que detém a concessão
- **THEN** a evidência mostra expiração da autoridade anterior e aquisição por outro backend sem alteração prévia da composição

#### Scenario: Inicialização pelo script existente
- **WHEN** o desenvolvedor executa `./scripts/dev.sh` sem configuração de produção
- **THEN** a topologia completa inclui o atuador Docker pronto para congelar e retomar os componentes cadastrados, sem passos manuais adicionais

## ADDED Requirements

### Requirement: Logs operacionais legíveis
O balanceador SHALL omitir de seu access log somente respostas bem-sucedidas das leituras periódicas usadas para atualizar o painel administrativo. Erros dessas consultas, mutações administrativas, transferências, downloads e demais requisições SHALL continuar registrados.

#### Scenario: Atualização normal do painel
- **WHEN** as consultas periódicas de cluster e operações respondem com sucesso
- **THEN** elas não produzem uma linha de access log a cada intervalo

#### Scenario: Consulta administrativa falha
- **WHEN** uma consulta periódica administrativa responde com erro
- **THEN** o balanceador registra método, caminho, status e dados usuais de diagnóstico
