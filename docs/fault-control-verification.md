# Verificação do atuador de falhas

Rodada executada em 16/09/2026 com `./scripts/dev.sh -d` e o projeto Compose
`drive-clone-dev`. As ações passaram pela API administrativa e pelo atuador; nenhum
comando `docker pause` foi chamado pelo roteiro de prova.

## Resultados

- O ambiente iniciou os três nós, o Nginx e o `fault-actuator`. A projeção inicial
  mostrou três nós `ready`, três rotas e controles disponíveis.
- A parada do MinIO do nó 2 foi confirmada pelo Docker. O cluster observou
  `storage=false`, marcou o nó `unavailable` e o retirou da rota. A restauração
  devolveu o nó a `ready`; o ID do container permaneceu igual.
- A parada composta do nó 1 produziu resultados separados para backend, SQL e
  storage. A restauração ocorreu na ordem inversa e os três IDs foram preservados.
- Ao parar o nó gerenciador, o mandato passou de 5 para 6. A troca de gerenciador e
  a retirada da rota foram observadas em 28 segundos, sem liberação cooperativa do
  lease. O mesmo processo e os mesmos volumes retornaram depois da restauração.
- Uma conta comum recebeu HTTP 403 ao solicitar falha; uma sessão revogada recebeu
  HTTP 401. A contagem de ações do atuador não mudou.
- Durante um upload de 3 MiB em três partes, o storage do nó 3 foi pausado após a
  segunda parte. A retirada ocorreu em 9,860 s. O cliente reenviou a parte para um
  sobrevivente e a publicação terminou em 40,209 s enquanto o container alvo ainda
  estava pausado. O SHA-256 baixado foi
  `3a335d679921f346f458f080f4d36ea28f02f233881986d7f6007398635b9a7c`.
  Depois da restauração e readmissão, o SQL confirmou três cópias; novo download
  produziu o mesmo SHA-256.

O teste de upload revelou que um worker podia manter uma tentativa contra um storage
já retirado. `uploads.Service` agora observa a versão de `cluster_configuration` e
cancela a cópia antiga quando a composição muda. A operação durável é então reclamada
com o snapshot atual e continua nos sobreviventes.

## Verificações complementares

`go test ./...`, os 42 testes do frontend, `npm run build`, os sete testes do Nginx
e o teste real de expiração do lease com CockroachDB passaram. O navegador mostrou
o diagrama com load balancer, três rotas, gerenciador, componentes, controles,
sequência do incidente, associação/retirada e detalhes técnicos recolhíveis.

## Prova descartável no Railway

A prova foi executada em 16/09/2026 contra uma instância descartável de
`nginx:latest`, identificada somente durante o ensaio pelo ID de instância fornecido
no painel. Uma chave Ed25519 dedicada foi cadastrada na conta e o acesso usou o
cliente OpenSSH diretamente, sem token de API e sem Railway CLI no caminho remoto.

A conexão confirmou `nginx` como PID 1, usuário remoto `root` e estado inicial `S`.
O comando `kill -STOP 1` retornou código zero, mas o estado permaneceu `S` na mesma
sessão e numa segunda sessão SSH independente. `SigPnd` e `ShdPnd` também ficaram
zerados. A segunda sessão foi aceita e `kill -CONT 1` retornou código zero, porém não
havia processo parado para retomar. Portanto, a premissa de congelar o PID 1 do
container por sinal é inválida neste ambiente e a tarefa 2.2 permanece aberta.

O gateway apresentou a chave Ed25519
`SHA256:+S1xg92FrnHz6pY3bpkmh1OGtWQGNANXilPzlxA7B1g`, que foi fixada num
`known_hosts` isolado durante o ensaio com `StrictHostKeyChecking=yes`. A Railway não
publica uma lista autoritativa e estável das chaves do gateway distribuído; uma
configuração de produção precisa definir explicitamente como inicializa e renova esse
conjunto sem desligar a verificação.

O adaptador `railway-ssh` continua respondendo 503. Não foi criada simulação nem
implementado um caminho que declararia sucesso sem tornar o serviço indisponível.

### Validação do init externo e do grupo de processos

Uma segunda imagem descartável colocou `tini` como PID 1 e iniciou o Nginx como seu
único filho direto. O PID numérico do filho variou no teste local, por isso a prova
resolveu o filho por `/proc/1/task/1/children` e atuou sobre todos os processos de seu
grupo, sem procurar nome de executável e sem receber comando ou PID do cliente.

Localmente, mestre e workers passaram de `S` para `T`, uma leitura HTTP expirou em
dois segundos, uma execução separada enviou `SIGCONT` e a leitura voltou a responder.
Na instância Railway, o grupo inteiro passou para `T` às 19:21:58. A primeira sessão
terminou com código zero às 19:22:01. Uma segunda sessão SSH foi aceita enquanto o
workload permanecia parado, confirmou timeout HTTP, retomou o mesmo grupo e observou
todos os processos novamente em `S`; o HTTP respondeu e a sessão terminou com código
zero às 19:22:07.

A hipótese técnica está validada: manter um init mínimo como PID 1 permite congelar e
retomar abruptamente o grupo do workload por uma nova sessão Railway. A spec ainda
precisa substituir os comandos contra PID 1 por um helper fixo que valide o único
filho direto e sinalize seu grupo; até essa revisão, 2.2 e 2.3 continuam abertas.
