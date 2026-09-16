## Quadro de execução

| Frente | Branch | Worktree | Responsabilidade | Estado |
| --- | --- | --- | --- | --- |
| Coordenação | `main` | raiz do projeto | contratos, backend, Compose, integração e evidências | alternativa validada localmente e no Railway com `tini` e sinal no grupo filho; aguarda revisão da spec antes de 2.2/2.3 |
| Painel | `implementation/fault-dashboard` | `/tmp/acervo-fault-dashboard` | tarefas 4.1–4.4, somente `frontend/` | integrado em `b0131c7` e `8b50e15`; 42 testes e build OK |
| Nginx | `implementation/fault-nginx` | `/tmp/acervo-fault-nginx` | tarefa 5.1, somente `nginx/` | integrado em `9f832b1`; 7 testes OK |
| Atuador | `implementation/fault-actuator` | `/tmp/acervo-fault-actuator` | tarefas 1.2, 1.3, 2.1 e 2.4, pacote isolado | integrado em `974c8eb`, `1df0531` e `102f52b`; race, vet e imagem OK |
| SSH Railway | `implementation/railway-ssh-adapter` | `/tmp/acervo-railway-ssh-adapter` | adaptador OpenSSH e testes da tarefa 2.3, somente `backend/` | em execução após prova 2.2 validada |
| Runtime Railway | `implementation/railway-fault-runtime` | `/tmp/acervo-railway-fault-runtime` | helper de sinais e Dockerfiles Railway das tarefas 2.3/6.2 | em execução após prova 2.2 validada |

## 1. Contrato do atuador e projeção administrativa

- [x] 1.1 [Coordenador, contrato compartilhado] Definir os tipos de alvo, ação, estado e erro sanitizado entre backend, atuador e frontend; verificar serialização, rejeição de valores desconhecidos e ausência de credenciais em testes de contrato.
- [x] 1.2 Implementar no atuador o mapeamento explícito de nó e componente e a seleção fechada de modo `docker` ou `railway-ssh`, recusando o próprio atuador, destinos ausentes e configurações cruzadas; verificar inicialização inválida, ausência de fallback e resolução exata em testes unitários.
- [x] 1.3 Implementar autenticação interna entre backend e atuador e autorização administrativa na entrada pública; verificar usuário comum, sessão expirada, chamada interna sem credencial e repetição idempotente.

## 2. Adaptadores de infraestrutura

- [x] 2.1 [Depende de 1.1 e 1.2] Implementar o adaptador Docker Compose para congelar e retomar backend, CockroachDB, MinIO e nó inteiro sem remover volumes; verificar por integração que os processos deixam de responder e retornam com a mesma identidade persistida.
- [x] 2.2 [Depende de 1.1 e 1.2] Fazer primeiro uma prova descartável de SSH no Railway com init mínimo e workload em grupo filho: enviar `SIGSTOP` ao grupo, abrir uma segunda sessão e enviar `SIGCONT`; registrar indisponibilidade e recuperação reais e interromper esta frente sem criar simulação substituta se a segunda sessão não for aceita.
- [ ] 2.3 [Depende de 2.2] Implementar o adaptador Railway com cliente OpenSSH, chave dedicada, IDs de instância cadastrados e comandos fixos do helper para `stop`, `restore` e `status`; verificar alvo desconhecido, topologia inesperada, host key, timeout, conexão interrompida, confirmação posterior e ausência de interpolação de entrada do cliente.
- [x] 2.4 Implementar operações compostas de nó inteiro com resultado individual por componente e restauração parcial segura; verificar falha intermediária, repetição e relatório fiel dos alvos congelados.

## 3. Detecção sem sinal cooperativo

- [x] 3.1 [Coordenador, depende de 2.1] Retirar `node_faults` do caminho de execução e remover a configuração `ENABLE_DEV_FAULTS`, preservando migrações antigas sem reutilizar a tabela; verificar que nenhuma ação administrativa altera membership, lease ou eventos do cluster antes da observação.
- [x] 3.2 Ligar as rotas administrativas ao atuador e expor separadamente estado da ação, observações e eventos sanitizados com mandato e detalhes conhecidos; verificar autorização, timeout, erro do provedor e que aceitação da parada não antecipa a exclusão.
- [x] 3.3 Verificar com o controlador real que a parada do backend gerenciador não libera o lease e que o sucessor assume apenas após expiração; verificar também retirada automática por parada isolada de CockroachDB e MinIO.

## 4. Diagrama e narrativa do incidente

- [x] 4.1 Substituir os cartões principais pelo diagrama responsivo de load balancer, rotas e nós com componentes, badge do gerenciador e estados acessíveis; verificar nós prontos, indisponíveis, sincronizando, removidos e observações desatualizadas em testes do frontend.
- [x] 4.2 Implementar seleção de nó com ações confirmadas e detalhes técnicos secundários, incluindo impacto esperado quando a ação puder remover quorum; verificar cancelamento, envio ocupado, sucesso parcial, erro e restauração.
- [x] 4.3 Construir a sequência do incidente a partir das fontes autoritativas do atuador e do cluster, sem concluir etapas por inferência; verificar parada ainda não detectada, exclusão, atualização de rota, restauração, sincronização e readmissão.
- [x] 4.4 Manter eventos técnicos em seção recolhível com detalhes sanitizados e operações pendentes abaixo do diagrama; verificar navegação por teclado, leitura por tecnologia assistiva e ausência de dados privados.

## 5. Balanceador e observabilidade operacional

- [x] 5.1 Adicionar ao Nginx logging condicional que omite somente `GET` administrativos periódicos com resposta bem-sucedida; verificar em teste de configuração que erros nesses endpoints, mutações e tráfego de arquivos continuam no access log.
- [x] 5.2 Expor ao painel a composição efetivamente roteada ou uma projeção autoritativa equivalente, sem tratar uma ação aceita pelo provedor como rota removida; verificar divergência temporária entre parada, exclusão e reload do Nginx.

## 6. Implantação e prova distribuída

- [x] 6.1 Adicionar o atuador ao Compose em modo Docker, montar o socket somente nesse serviço e documentar o mapeamento local; verificar que `./scripts/dev.sh` continua iniciando sozinho a topologia completa, que os três nós podem ser congelados e retomados e que os volumes são preservados.
- [ ] 6.2 Documentar e validar a topologia Railway com serviços separados, Dockerfiles com init/helper, IDs de instância configurados, chave SSH dedicada no atuador e `known_hosts` inicializado em sessão controlada; confirmar que nenhum token de API ou Railway CLI existe no runtime e verificar configuração incompleta sem atingir um serviço real.
- [x] 6.3 [Coordenador, depende de 3.3, 4.3, 5.2 e 6.1] Executar a prova local com upload em andamento, parada real de um storage, continuidade nos sobreviventes, restauração, sincronização e readmissão; registrar comandos, tempos observados e checksums.
- [x] 6.4 Executar a prova local parando o backend gerenciador e depois um nó inteiro, confirmando sucessão por expiração do lease, mudança de rotas e recuperação sem marca cooperativa; registrar evidências e qualquer limite do ambiente.
- [x] 6.5 Atualizar README e roteiro da demonstração para distinguir ação do provedor, observação do cluster e decisão automática; verificar que nenhuma instrução ainda descreve `node_faults` ou `ENABLE_DEV_FAULTS` como mecanismo da demonstração.
