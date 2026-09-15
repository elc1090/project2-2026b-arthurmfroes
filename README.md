# Drive Clone

## Proposta

Clone do Google Drive com funcionalidades alinhadas aos requisitos do segundo projeto.

### Requisitos funcionais

- Cadastro e autenticação de usuários.
- Upload e download de arquivos.
- Suporte a pastas e subpastas.
- Arquitetura distribuída com múltiplas réplicas de backend.
- Sincronização e consistência dos arquivos entre as réplicas.
- Resiliência à indisponibilidade de uma réplica.
- Suporte opcional à criptografia dos arquivos.
- Visualização simples do estado dos backends para fins de demonstração.
- Mecanismo simples para simular falhas em uma ou mais réplicas.

## Tecnologias escolhidas

- React
- Golang
- Banco SQL
- Sistema de object storage
- Load balancer

## Ambiente de desenvolvimento

O `docker-compose.dev.yml` reproduz a topologia planejada para o Railway com serviços
separados:

- um load balancer Nginx exposto em `http://localhost:8080`;
- três backends chamados `backend-node-1`, `backend-node-2` e `backend-node-3`;
- três nós CockroachDB com volumes próprios;
- três MinIO independentes com volumes próprios e replicação entre sites;
- inicializadores para criar o cluster CockroachDB, o banco `drive_clone`, a replicação do
  MinIO e o bucket `drive-clone`.

```bash
./scripts/dev.sh
```

Para executar em segundo plano, use `./scripts/dev.sh -d`.

Os painéis dos três nós CockroachDB ficam nas portas `8088`, `8089` e `8090`. As portas SQL
correspondentes são `26257`, `26258` e `26259`.

Os consoles dos MinIO ficam nas portas `9001`, `9011` e `9021`. Suas APIs ficam nas portas
`9000`, `9010` e `9020`. Cada backend grava no MinIO de mesmo número, e o MinIO replica os
objetos para os outros dois sites.

Dentro da rede do Compose, cada backend, nó CockroachDB e MinIO possui identidade, endereço
e volume próprios. O Nginx acessa os backends pelos nomes internos do Compose.
