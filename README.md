# Acervo

Acervo é um projeto acadêmico de armazenamento distribuído de arquivos. A interface
permite cadastro com login e senha, pastas e subpastas, seleção múltipla para upload,
retomada de transferências e download. O painel administrativo acompanha os nós e
permite demonstrar falhas sem assumir o controle automático do cluster.

A aplicação usa React/TypeScript, Go, CockroachDB, MinIO e Nginx. Cada nó lógico tem
backend, acesso ao seu banco local e armazenamento MinIO próprio. Os bancos formam
um único cluster SQL; os storages possuem volumes independentes e cooperam por
replicação. O gerenciador é eleito entre os backends por uma concessão no SQL.

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

```sh
docker compose -f docker-compose.dev.yml ps -a
docker compose -f docker-compose.dev.yml logs --tail=100 backend-node-1 load-balancer
docker compose -f docker-compose.dev.yml stop
```

O último comando para os serviços preservando os volumes. A inicialização pode
levar algum tempo: um processo vivo ainda não está necessariamente admitido para
atender usuários. A entrada retorna 503 quando não dispõe de nós elegíveis.

| Serviço local | Portas |
| --- | --- |
| Interface e API via Nginx | 8080 |
| Cockroach SQL, nós 1/2/3 | 26257 / 26258 / 26259 |
| Painéis Cockroach, nós 1/2/3 | 8088 / 8089 / 8090 |
| APIs MinIO, nós 1/2/3 | 9000 / 9010 / 9020 |
| Consoles MinIO, nós 1/2/3 | 9001 / 9011 / 9021 |

O Compose fixa três nós para a demonstração local. Cada backend usa o Cockroach e
MinIO correspondentes; o Nginx recebe do gerenciador a composição elegível. O nome
interno do projeto Compose continua `drive-clone-dev`, o banco `drive_clone` e o
bucket `drive-clone`, preservando a configuração dos volumes existentes.

O atuador fica acessível somente na rede do Compose e é o único serviço que monta
`/var/run/docker.sock`. Os botões administrativos pedem que ele pause ou retome o
container cadastrado. O gerenciador não recebe essa intenção: detecta a ausência
pelas sondagens, altera a composição e o Nginx publica as novas rotas.

## Transferências e consistência

O seletor aceita vários arquivos, de qualquer tipo, inclusive vazios. A interface
prepara SHA-256 completo e manifesto incremental em um worker, lendo partes de
32 MiB. A fila inteira permite dois envios de partes simultâneos. O limite de 100M
no Nginx se aplica a cada requisição; não é um limite do tamanho total do arquivo.

`Enviando`, `Confirmando armazenamento` e `Concluído` são estados distintos. Receber
todos os bytes não publica o arquivo: a publicação exige recibos de conteúdo idêntico
em todos os sites obrigatórios da configuração vigente. Uma resposta MinIO obtida
por proxy de outro site não é prova de cópia local.

Com a página aberta, falhas transitórias permitem consulta e repetição idempotente.
Após reabrir e entrar, o backend recupera a fila. Se faltarem bytes, selecione o
original novamente; o worker compara o conteúdo, não apenas nome e tamanho. Arquivos
com bytes preservados podem concluir no backend com o navegador fechado. Downloads
usam a transferência nativa do navegador, sem montar o arquivo inteiro em JavaScript.

## Configuração e administração

As variáveis do backend estão descritas em [docs/demo.md](docs/demo.md). Os três nós
iniciais usam `NODE_BOOTSTRAP=true`; um nó adicional já provisionado usa `false` e
aguarda registro administrativo. O formulário recebe endpoints genéricos, não cria
máquinas e não exige configuração de provedor específico.

`ADMIN_PROFILES_FILE` aponta para perfis administrativos privados do servidor.
Credenciais não são informadas no painel. Associação, retirada e recuperação têm
etapas persistidas; concluir a associação não substitui a sincronização necessária
para admissão. Falhas temporárias removem o nó automaticamente do atendimento, sem
apagar volumes. Retomar o container não promove o nó diretamente para `ready`:
saúde, sincronização e readmissão continuam sob responsabilidade do cluster.

## Verificações e roteiro

Para validar o código local, com Go compatível com `backend/go.mod`, Node compatível
com Vite e Python 3:

```sh
(cd backend && go test ./...)
npm --prefix frontend ci
npm --prefix frontend test
npm --prefix frontend run build
python3 -m unittest discover -s nginx -p 'test_*.py'
openspec validate implement-distributed-drive --strict
```

Testes dependentes de SQL, S3 ou Docker precisam de seu ambiente de integração;
um teste pulado não comprova comportamento distribuído.

As evidências registradas incluem [bootstrap e persistência MinIO](docs/infrastructure-verification.md),
[a primeira passagem pelo navegador](frontend/verification/browser-first-pass.md)
e [publicação e retomada](frontend/verification/browser-resume-and-faults.md).
A segunda passagem verificou download de 34 MiB com SHA-256 idêntico, rejeição de
arquivo reselecionado divergente, erro isolado e sucessão do gerenciador pelo painel.

O [roteiro de demonstração](docs/demo.md) distingue essas evidências das verificações
pendentes. A associação, troca de volume e retirada de um quarto nó foram verificadas.
A suíte de falhas passou em 20 cenários, com publicação e download pelos
sobreviventes. A fase de partições reais passou, incluindo sucessão e perda de quorum.
A [retomada multiparte após reabertura](frontend/verification/browser-recovery-fresh-result.md)
preservou a parte já enviada e verificou os downloads. Também
passaram os [downloads interrompidos sem autoridade](docs/authority-loss-verification.md)
e a [transferência de 256 MiB com medição de memória](frontend/verification/memory-256-result.md),
com SHA-256 conferido em cada MinIO isolado. A [queda do worker durante cópia](scripts/worker-recovery-test.md) e a
[publicação durante sincronização](docs/publication-during-sync-verification.md)
foram comprovadas, incluindo integridade física por site. A comparação com 2 GiB
permanece pendente.
**2 GiB é referência de teste, não teto de arquivo.** O teste real desse tamanho
está programado após as provas de recuperação; o espaço necessário foi liberado.

O [script de desenvolvimento](docs/dev-entrypoint-verification-plan.md) foi executado
com os defaults do Compose. Subida, reinício, sessão compartilhada e persistência
do arquivo passaram pelos três backends e pelo Nginx.

A [prova do atuador real](docs/fault-control-verification.md) registra pausa de
storage, nó completo e gerenciador, sucessão por expiração do lease e publicação
de upload pelos sobreviventes com checksum idêntico após a readmissão.

Requisitos e tarefas ficam em [OpenSpec](openspec/changes/add-real-fault-control-and-cluster-diagram/).
A política de agentes e worktrees fica em [docs/development-workflow.md](docs/development-workflow.md).
