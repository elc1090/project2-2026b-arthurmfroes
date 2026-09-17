# Projeto: Aplicação com persistência de dados em backend

> 1. Leia com atenção as instruções abaixo para editar este README em formato Markdown.
> 2. Substitua todos os trechos de texto iniciados com "Substitua" por informações do seu projeto, conforme solicitado em cada trecho.
> 3. Substitua a imagem animada por um GIF/WEBP mostrando o resultado do seu projeto (o arquivo pode ser armazenado no repositório ou em URL externa). 
> 4. Remova todas as instruções de entrega.
> 5. Renomeie esta arquivo para README.md e entregue-o dentro da pasta raiz do seu repositório de entrega. 
> 6. Double-check: Certifique-se de que seu README.md não contenha instruções de entrega e seja visualizado corretamente ao abrir seu repositório!
> Opcional: você pode alterar a formatação deste README, mas mantenha todas as informações solicitadas.

![Substitua a imagem ao lado por um GIF/WEBP animado mostrando seu projeto](./moho_follow_through2.gif "GIF animado do projeto. Imagem temporária de Moho Animation https://moho.lostmarble.com/products/moho-pro-special-halls-head-college")



## Acesso

https://load-balancer-production-ca4f.up.railway.app/

## Desenvolvedor(a)
Arthur Moro Fróes - Sistemas de informação


## Proposta
Modalidade onde um colega é um cliente

A proposta consistiu em um clone do Google Drive, mas com arquitetura distribuída e uma maneira didática de visualizar esses conceitos.

### Funcionalidades previstas:
- Cadastro e autenticação de usuários.
- Upload e download de arquivos.
- Suporte a pastas e subpastas.
- Arquitetura distribuída com múltiplas réplicas de backend.
- Sincronização e consistência dos arquivos entre as réplicas.
- Resiliência à indisponibilidade de uma réplica.
- Visualização simples do estado dos backends para fins de demonstração.
- Mecanismo simples para simular falhas em uma ou mais réplicas.

## Parceria/cliente/usuário
Fabrício Thomas Freitas Santos

## Feedback/comentário da parceria/cliente/usuário
A proposta e desenvolvimento se alinham com o que foi esperado pelo projeto

## Desenvolvimento

### Processo

A ideia do projeto era aprender principalmente duas coisas: colocar a mão na massa em sistemas distribuídos, que eu não tinha quase experiência prática e também conhecer frameworks de desenvolvimento com IA, nesse caso o OpenSpec. Julgo que foi um sucesso, porque consegui aprender um pouco sobre ambos.

Comecei o desenvolvimento procurando e entendendo os frameworks disponíveis no mercado e comumente usados. Para propósitos da disciplina, cheguei a conclusão que o OpenSpec se alinhava melhor. Depois de escolhido, comecei o processo de idealização da proposta e a transcrição disso em specs criadas. A ideia do framework é sair de uma ideia não necessariamente técnica, mas não gosto muito dessa abordagem. Tinha em mente uma idéia prévia do projeto, então começamos daí. Com as specs escritas, deleguei a construção para os agentes de codigo que tenho acesso (família gpt). Foram necessários ajustes depois das primeiras entregas, através de iterações com o próprio agente. Gostei bastante do framework, resolvi também utilizar ele no trabalho como uma alternativa ao framework personalizado que utilizamos na empresa e os resultados foram satisfatórios. 

As partes mais difíceis foram:

- Entender como se faz a distribuição do sistema sem criar complexidade desnecessária
- Entender a comunicação entre os nós e a escolha de gerentes
- Implementar de maneira consistente a infraestrutura tanto em modo de desenvolvimento quanto em produção. O railway facilitou muito nisso, mas ainda assim foi um processo chato

Me sinto mais pronto pra criar e manter sistemas desse tipo no futuro, mas definitivamente não é simples.

### Trechos de código

#### Simulação de uma falha real de processo

O painel administrativo envia a solicitação ao `fault-actuator`, que acessa o serviço correspondente por SSH. Dentro do container, o comando `fault-signal` envia `SIGSTOP` ou `SIGCONT` para todo o grupo de processos da aplicação:

```go
switch args[0] {
case "stop":
    if err := signal(-workload.pgrp, syscall.SIGSTOP); err != nil {
        fmt.Fprintln(stderr, "could not stop workload process group")
        return 1
    }
    if err := waitForState(root, true); err != nil {
        fmt.Fprintln(stderr, "workload stop was not confirmed")
        return 1
    }
    fmt.Fprintln(stdout, "stopped")
case "restore":
    if err := signal(-workload.pgrp, syscall.SIGCONT); err != nil {
        fmt.Fprintln(stderr, "could not restore workload process group")
        return 1
    }
    if err := waitForState(root, false); err != nil {
        fmt.Fprintln(stderr, "workload restore was not confirmed")
        return 1
    }
    fmt.Fprintln(stdout, "running")
}
```

O PID negativo faz o sinal atingir o grupo inteiro. O processo não recebe uma chamada da aplicação para se desligar e não avisa o cluster antes de parar. Isso permite observar a detecção da falha, uma nova eleição quando necessário, a retirada da rota e a posterior sincronização. O código completo está em [`backend/cmd/fault-signal/main.go`](backend/cmd/fault-signal/main.go).

#### Estado atual dos nós e configuração publicada

Cada backend consulta no CockroachDB a configuração atual considerada válida pelo sistema. A consulta traz o estado observado de todos os nós e informa quais deles pertencem à composição vigente:

```go
rows, err := tx.Query(ctx, `SELECT
    n.id::STRING,
    n.node_id,
    n.backend_endpoint,
    n.database_endpoint,
    n.storage_endpoint,
    n.storage_generation::STRING,   
    n.state,
    m.node_id IS NOT NULL
FROM cluster_nodes n
LEFT JOIN cluster_membership m ON n.id=m.node_id
ORDER BY n.node_id`)

for rows.Next() {
    var n cluster.Node
    var member bool
    if err := rows.Scan(&n.ID, &n.NodeID, &n.BackendEndpoint,
        &n.DatabaseEndpoint, &n.StorageEndpoint,
        &n.StorageGeneration, &n.State, &member); err != nil {
        return err
    }
    result.Nodes = append(result.Nodes, n)
    if member {
        result.Configuration.Members = append(result.Configuration.Members, n)
    }
}
```

A consulta devolve uma linha para cada nó registrado. Em cada repetição, `n` representa o nó lido naquela linha. O primeiro `append` adiciona esse nó à lista `Nodes`, que reúne todos os nós, inclusive os que estão entrando, sincronizando, indisponíveis ou retirados. Se `member` for verdadeiro, o segundo `append` também adiciona o mesmo nó à lista `Configuration.Members`, que contém somente os nós admitidos para receber tráfego. Portanto, o laço monta as duas listas para a resposta de `/internal/cluster`; ele não altera o banco nem adiciona apenas o próprio backend. Antes de devolver essa resposta, o backend confirma que existe um gerenciador eleito e que o prazo da autoridade dele ainda não expirou. Esse mecanismo está em [`backend/internal/control/http.go`](backend/internal/control/http.go).

#### Atualização automática das rotas do load balancer

O load balancer consulta o endpoint interno de controle dos backends e aceita somente uma configuração com versão e prazo válidos:

```python
request = urllib.request.Request(
    raw.rstrip("/") + "/internal/cluster",
    headers={
        "Authorization": "Bearer " + self.token,
        "Cache-Control": "no-cache",
    },
)

version, ttl, backends = snapshot(data)
expires = started + min(ttl, self.max_stale)
if (
    expires <= self.clock()
    or version < self.version
    or (version == self.version and backends != self.backends)
):
    continue

self.version, self.backends, self.deadline = version, backends, expires
self.revoked = False
return backends
```

O Nginx não procura diretamente qual backend é o gerenciador. Ele pode consultar qualquer backend saudável, pois todos leem o mesmo estado no CockroachDB. A resposta só é válida enquanto existe um gerenciador eleito com autoridade vigente. Se o prazo expira sem uma nova configuração, o reconciliador remove as rotas em vez de continuar usando uma composição antiga. O código está em [`nginx/reconcile.py`](nginx/reconcile.py).


## Tecnologias

### Linguagens e afins

- Golang
- Minio
- CockroachDB
- Nginx

### Ambiente de desenvolvimento

- VsCode
- Codex CLI

## Executar em desenvolvimento

É necessário Docker com o plugin Compose e recursos para três bancos, três storages,
três backends, o atuador de falhas e a entrada Nginx. O script constrói as imagens,
incluindo a interface, e inicia o Compose de desenvolvimento:

```sh
./scripts/dev.sh
```

Em segundo plano:

```sh
./scripts/dev.sh -d
```

Abra **http://localhost:8080**. Crie uma conta com login e senha. O Compose provisiona
a conta administrativa acadêmica `admin` / `admin`. As credenciais locais e o
Cockroach em modo inseguro pertencem a esse ambiente de desenvolvimento. Não há
configuração de implantação em produção nesta versão.


## Executar em produção

O primeiro deploy completo no Railway pode ser conduzido pelo roteiro interativo:

```sh
./scripts/setup-railway.sh
```

Para adicionar e acompanhar a admissão de outro nó sem usar o painel do provedor:

```sh
./scripts/add-railway-node.sh
```
