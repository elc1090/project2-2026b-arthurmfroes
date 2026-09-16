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

O adaptador Railway continua pendente. A change exige provar em uma instância
descartável que uma segunda sessão SSH consegue enviar `kill -CONT 1` depois que a
primeira envia `kill -STOP 1`. Sem essa evidência, `railway-ssh` responde 503 e não
simula a falha.
