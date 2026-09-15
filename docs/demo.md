# Demonstração do Acervo

Este roteiro usa somente desenvolvimento. Os comandos descrevem como reproduzir
cenários; não significam que cada cenário já passou. As evidências executadas e as
pendências aparecem ao final.

## Abrir o ambiente

Na raiz do repositório, execute `./scripts/dev.sh -d` e abra
`http://localhost:8080`. O script equivale a `docker compose` com
`docker-compose.dev.yml`, `up --build` e os argumentos recebidos. Não é necessário
iniciar Vite separadamente: o Dockerfile do Nginx constrói e serve a interface.

Confira `docker compose -f docker-compose.dev.yml ps -a` e os logs. Os inicializadores
formam o cluster SQL, criam o banco e verificam os sites MinIO. Em reinício degradado,
os sobreviventes não dependem do sucesso de um inicializador global para iniciar;
a admissão ainda exige saúde, autoridade SQL e sincronização.

O modo local usa Cockroach sem TLS, MinIO `minioadmin` / `minioadmin` e administrador
`admin` / `admin`. A conta normal é criada na interface, sem e-mail. O papel
administrativo é provisionado pelo backend, não escolhido no cadastro.

## Arquivos e retomada

1. Crie uma conta, uma pasta `Pesquisa` e uma subpasta `Exemplos`. Confira o caminho
   nas migalhas e navegue entre a raiz e as duas pastas.
2. Selecione juntos um texto, um binário de extensão desconhecida e um arquivo vazio.
   Acompanhe preparação, envio e confirmação em linhas independentes. Navegar para
   outra pasta deve manter a fila.
3. Aguarde `Concluído`, baixe pelo link nativo e compare o original e o download com
   `sha256sum`. Recebimento das partes ou 100% de envio não substitui a confirmação.
4. Para demonstrar erro isolado, tente enviar um nome já existente junto com um nome
   novo. O conflito deve afetar somente a primeira transferência.
5. Para demonstrar retomada, interrompa temporariamente a conexão do navegador durante
   o envio, sem apagar dados. Com a página aberta, observe espera e novas consultas.
   Após recarregar, entre novamente e selecione o original quando solicitado.
6. Selecione primeiro uma cópia de mesmo nome/tamanho com conteúdo alterado. Ela deve
   ser rejeitada. Selecione depois o original e confira a publicação e o download.
   Se todos os bytes já estavam preservados, a confirmação pode terminar sem reenvio.

Na passagem automatizada registrada, a interrupção foi feita por Playwright no PUT
de uma parte. Isso testa o transporte do navegador; não é uma partição real entre
servidores. Logout deve retirar da tela a fila privada da conta anterior.

## Falhas no painel

Entre com a conta administrativa e abra Administração. Observe identidade, estado,
componentes, horário de observação/transição, configuração, gerenciador e mandato.
Observação ausente ou desatualizada não deve parecer uma medição atual.

Escolha **um nó de cada vez**, provoque falha de storage e aguarde a retirada automática.
O painel separa a confirmação da solicitação do estado observado. Restaure com
`Restaurar nó`; aguarde sincronização e retorno a `Pronto` antes do próximo cenário.
Repita o procedimento para uma falha total no gerenciador e observe a sucessão.

O painel também oferece backend, banco local e comunicação de controle. A simulação
só aparece quando `ENABLE_DEV_FAULTS=true`. As operações do gerenciador continuam com
o painel fechado. Processos mortos e rede Docker interrompida exigem uma rodada de
teste própria, coordenada com quem usa os mesmos containers.

A área de operações mostra UUID, fase, recebimentos, recibos e sites pendentes.
Ela não fornece nomes, caminhos ou download de arquivos de outro usuário.

## Associar e retirar um nó provisionado

O quarto nó precisa ter backend, Cockroach e MinIO acessíveis pelos demais serviços.
O Cockroach deve participar do mesmo cluster SQL. O MinIO novo deve seguir as
condições de associação de site, sem inicialização manual de buckets para contornar
erros. Use `NODE_BOOTSTRAP=false` no backend novo e mantenha o token de controle
compatível. O endpoint interno de identidade permite ao servidor conferir os dados
informados no painel.

No formulário, informe identidade, endpoint do backend, endpoint SQL e endpoint do
storage sem userinfo ou query. O perfil inicial é `default`. A operação retorna
aceitação assíncrona: acompanhe preflight, SQL e storage. O nó só atende depois da
sincronização e da barreira de admissão, mesmo quando as etapas de associação terminam.

A retirada também tem preflight e etapas externas. A configuração de três réplicas
SQL pode impedir retirar um dos únicos três destinos. Saúde dos sobreviventes não
elimina esse impedimento. Não reduza a política de réplicas para fazer o roteiro passar.
Apenas intenção de retirada ainda em preflight oferece cancelamento; etapas externas
já iniciadas não são tratadas como seguramente reversíveis. Os volumes são preservados.

O roteiro técnico isolado está em [scripts/fourth-node-test.md](../scripts/fourth-node-test.md).
A rodada de quatro nós passou, incluindo troca de volume e retirada pelo painel;
o relatório está em [browser-node-retirement.md](../frontend/verification/browser-node-retirement.md). Recuperação automática de storage substituído aparece como
`replace` e pode remover a associação antiga antes de sincronizar a nova.

## Variáveis do backend

Os valores do Compose são definidos nos blocos `environment`. Para personalizá-los,
altere essa configuração ou use um override explícito. Exportar uma variável no
shell não substitui automaticamente um valor literal do Compose.

| Variável | Função |
| --- | --- |
| `NODE_ID`, `BACKEND_ENDPOINT` | Identidade e origem HTTP anunciadas pelo nó |
| `PORT` | Porta HTTP local, padrão 8080 |
| `DATABASE_URL` | Conexão privada ao gateway SQL local |
| `S3_ENDPOINT`, `S3_BUCKET` | Storage local e bucket da aplicação |
| `S3_ACCESS_KEY`, `S3_SECRET_KEY` | Credenciais privadas do storage |
| `CONTROL_TOKEN` | Autenticação compartilhada dos canais internos |
| `CONTROL_INTERVAL`, `CONTROL_TIMEOUT` | Intervalo e timeout de sondagem, padrões 2s |
| `MANAGER_LEASE_TTL` | Duração da concessão, padrão 15s |
| `FAILURE_THRESHOLD` | Número configurado de falhas para retirada, padrão 3 |
| `NODE_BOOTSTRAP` | Autorregistro dos nós iniciais; padrão false, true nos três do Compose |
| `ADMIN_LOGIN`, `ADMIN_PASSWORD` | Provisionamento da conta administrativa, definidos juntos |
| `ADMIN_PROFILES_FILE` | Caminho de arquivo JSON privado com perfis de administração remota |
| `ENABLE_DEV_FAULTS` | Habilita simulação acadêmica; padrão false, true no Compose |
| `SECURE_COOKIES` | Atributo Secure da sessão; false na entrada HTTP local |

Os padrões são parâmetros de controle, não um prazo total exato garantido de detecção:
sondagens, eleição, transações e atualização do Nginx também consomem tempo.
O Nginx possui variáveis próprias, descritas em [nginx/README.md](../nginx/README.md).

Sem arquivo de perfis, `default` usa o gateway de `DATABASE_URL` e as credenciais S3
do executor. `ADMIN_PROFILES_FILE` aceita um objeto JSON cujas chaves são nomes de
perfil. Cada valor contém `sql_gateway`, `certs_dir`, `insecure`, `access_key` e
`secret_key`. Com `insecure=false`, `certs_dir` é obrigatório. O gateway usado pela
operação administrativa deve sobreviver à retirada; o arquivo e os certificados
precisam estar acessíveis ao backend executor. Não colocar esses segredos no frontend
ou versionar um arquivo preenchido. O Compose inicial não monta um arquivo de perfis.

## Evidências e limites atuais

Em 15/09/2026, os relatórios do repositório registram:

- Infraestrutura isolada com três gateways SQL, volumes independentes, bootstrap
  reexecutado, persistência MinIO com peers parados e demonstração de HEAD/GET por proxy.
- Navegador real com cadastro, pastas/subpastas, tipos diferentes, arquivo vazio,
  transferência de 34 MiB, publicação com navegador fechado e download com SHA igual.
- Retomada após recarga, conteúdo divergente rejeitado, conflito por arquivo e
  simulações de storage e falha total do gerenciador com restauração dos três nós.

Os relatórios são [infraestrutura](infrastructure-verification.md),
[primeira passagem](../frontend/verification/browser-first-pass.md) e
[retomada/falhas](../frontend/verification/browser-resume-and-faults.md).
Testes com respostas simuladas validam contratos locais, não essas condições distribuídas.

A associação e retirada de um quarto nó passaram, assim como a troca automática
de seu volume. A fase de partições reais passou com sucessão, rejeição sem quorum
e restauração. A prova de falha durante cópia foi concluída na rodada posterior descrita abaixo.
Permanece pendente a comparação de memória de 256 MiB com 2 GiB.
O [procedimento de medição](../frontend/verification/memory-procedure.md) registra
RSS/PSS da sessão Chromium e memória dos backends com os mesmos parâmetros de partes.
Os máximos amostrados não são picos contínuos; o contador de pico do cgroup inclui
sua carga anterior. O caso de 2 GiB aguarda sua janela exclusiva. O usuário liberou espaço suficiente
para as versões e cópias transitórias. Isso não introduz um teto
artificial de tamanho na aplicação.

O [resultado de 256 MiB](../frontend/verification/memory-256-result.md) registra
446 amostras de memória, publicação, download nativo e SHA-256 físico por site
com os outros dois MinIO desligados. A [prova de perda de autoridade](authority-loss-verification.md)
confirmou interrupção de downloads já iniciados e 503 nas novas requisições, tanto
no LB quanto nos backends. A comparação com 2 GiB continua pendente.

A prova de queda durante cópia ficou inconclusiva e consumiu a margem de disco: com
4,4 GiB livres, MinIO recusou até uma escrita de 13 bytes com `XMinioStorageFull`.
Os backends bloquearam atendimento. O coordenador recuperou o ambiente sem remover
volumes, liberando cache Go gerado e cancelando a operação do fixture pela API.
O [roteiro do worker](../scripts/worker-recovery-test.md) agora exige 7,5 GiB livres
iniciais e monitora um piso de 6 GiB. Esses limites pertencem ao teste isolado.
As tentativas de montagem podem gerar versões físicas adicionais; nesta rodada,
o objeto de 256 MiB ocupava 4,25 GiB lógicos somados entre versões dos três sites,
embora o catálogo tivesse uma única publicação. Não há coleta dessas versões
finais antigas nesta implementação; a limpeza existente cuida dos temporários.


Após o reset autorizado dos dados de teste, a suíte completa passou em 20 cenários
de falhas, com 138 requisições de transferência sem erro nessa rodada. O
[reteste de retomada](../frontend/verification/browser-recovery-fresh-result.md)
enviou 33 MiB, reabriu a página sem estado local e reaproveitou a parte de 32 MiB
já preservada. Os downloads dos dois arquivos selecionados tiveram SHA-256 igual
à origem. A descoberta da fila repetiu os dois 503 injetados sem intervenção.

A prova de queda durante cópia foi concluída após corrigir os observadores do
roteiro: o sucessor publicou 96 MiB em 33,77 segundos e o retorno do worker antigo
não duplicou a publicação. A publicação concorrente à sincronização também foi
comprovada. Os dois arquivos tiveram suas versões físicas lidas em cada MinIO com
os outros peers parados. Os relatos anteriores de tentativas inconclusivas ficam
preservados como histórico, separados dessas provas concluídas.


A [prova do script real](dev-entrypoint-verification-plan.md) passou na primeira
subida e no reinício, preservando arquivo, pasta e sessão compartilhada entre nós.
O Compose foi usado sem limites externos. A [associação pelo painel](../frontend/verification/browser-node-registration-result.md)
também passou, incluindo download pelo proprietário no quarto nó e retirada
confirmada no Cockroach e nos peers MinIO.
