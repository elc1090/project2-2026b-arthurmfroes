## Context

Ver proposal.md para motivação. O commit 0089187 contém apenas infraestrutura: módulo Go sem dependências e sem arquivos .go; Dockerfile esperando cmd/server; Nginx gerando upstream na partida com detecção passiva; três bancos e três MinIO; inicializadores e script de desenvolvimento. Não existe frontend, gerenciador, esquema SQL ou teste de integração.

A inspeção identificou diferenças concretas em relação aos requisitos: todos os DATABASE_URL apontam para cockroach-1; depends_on exige inicializadores globais concluídos para qualquer backend subir; minio/init-dev.sh verifica somente sucesso de replicate info, sem conferir todos os peers; o README atribui volumes aos backends, que não têm volumes; a replicação MinIO configurada não confirma publicação da aplicação.

## Goals / Non-Goals

**Goals:** implementar os contratos das seis specs com configuração portátil e coordenação única, garantindo que uma falha parcial retire o nó inteiro automaticamente. Preservar a simetria dos processos de aplicação.

**Non-Goals:** consenso próprio, fusão de bancos independentes, provisão de máquinas pelo painel e operação de escrita sem quorum. Standalone significa implantação independente do conjunto local; um nó isolado de um cluster de três bancos não continua confirmando escritas. A topologia base de desenvolvimento é de três nós; a associação dinâmica também será verificada com um quarto nó previamente provisionado. Um modo inicial de apenas um nó precisa de planejamento próprio para a política de replicação do banco.

## Decisions

### 1. Mesmo binário nos três backends

Criar backend/cmd/server com PORT, NODE_ID e endpoints locais. Cada backend usa o CockroachDB e MinIO do mesmo número. Falha do banco local desqualifica o nó; não esconder essa falha com fallback de requisições de usuário para outro banco. O controle interno pode alcançar os outros bancos para observar o estado comum.

Adicionar bibliotecas somente na etapa que as usa: driver SQL compatível com CockroachDB, cliente S3/admin e hash de senhas. Preferir HTTP para comunicação de controle. A presença de CLUSTER_* no Compose não obriga usar memberlist; membership nativo do banco já existe e heartbeat HTTP basta para o detector da aplicação.

### 2. Gerenciador eleito entre backends

Cada backend executa um candidato a gerenciador. Uma concessão temporária persistida no CockroachDB identifica o responsável e seu número de mandato. Transações serializáveis com retry protegem concessão, configuração e publicação. O relógio do banco determina vencimentos. Uma instância com mandato vencido não consegue alterar configuração.

O registro contém nós, endpoints, saúde por componente, estado, motivo, última observação e versão da configuração. Estados: joining, syncing, ready, unavailable e removed. Heartbeat e sondagem externa têm timeout, intervalo e limiar configuráveis; a suíte usa valores curtos explícitos e verifica o prazo de detecção mais propagação.

O gerenciador registra automaticamente indisponibilidade e incrementa a versão do conjunto elegível. A mudança não decommissiona o CockroachDB nem apaga um peer MinIO ou volume. Esses procedimentos são permanentes e diferem da exclusão temporária da aplicação. Réplicas MinIO podem continuar tentando entregar objetos a um site temporariamente ausente.

Alternativa rejeitada: decisões independentes em memória de cada backend, que poderiam permitir publicação com conjuntos contraditórios. Um gerenciador único sem sucessor criaria outra dependência central.

### 3. Balanceamento e autorização de atendimento

Manter o Nginx para entrada. Um reconciliador junto ao balanceador consulta a configuração confirmada, gera upstream em arquivo temporário, valida nginx -t e faz recarga atômica. Ele consulta qualquer backend de controle alcançável; não depende de backend-node-1. Configuração sem nós elegíveis produz resposta 503 válida, não upstream inválido.

Apenas recarregar Nginx é insuficiente: workers antigos e conexões persistentes podem ainda alcançar um nó retirado. O backend verifica elegibilidade antes de servir usuários, falha fechado quando não obtém estado atual e revalida mandato/configuração antes de confirmar mutações. Endpoints internos autenticados de saúde, reparo e gerenciamento permanecem disponíveis durante recuperação. Downloads já em andamento podem ser interrompidos; nenhuma operação nova deve ser admitida após retirada propagada. Não prometer detecção instantânea nem atomicidade de envio de bytes na rede.

Checks locais distinguem processo vivo de nó pronto. Avaliar acesso SQL com operação transacional curta, escrita/leitura de objeto de teste local, comunicação de controle e corrupção/falha do storage. Atraso transitório de um objeto ainda pending não deve causar expulsão em cascata: publicação aguarda cópias; falha persistente do componente desqualifica o nó. O motivo dos checks aparece no painel.

### 4. Confirmação por site e versão de aplicação

Manter MinIO site replication como transporte de replicação e recuperação, mas não usar seu sucesso de PUT original como confirmação de todos os sites. Nem ETag nem HEAD genérico são suficientes: ETag pode representar multipart e GET/HEAD pode consultar outro peer.

Cada upload usa chave de objeto imutável derivada do ID da operação, SHA-256 calculado sobre os bytes recebidos, tamanho e versão lógica da aplicação. A versão lógica é comum; versionId S3 pode diferir entre sites e é guardado por cópia. Uma chave nunca é sobrescrita com conteúdo diferente.

Para uma confirmação verificável, o componente de armazenamento de cada backend deve concluir uma escrita explícita no seu endpoint MinIO local, verificando SHA-256 e tamanho do fluxo antes de emitir um recibo persistido por site/operação. Usar reenvio idempotente do mesmo objeto quando a replicação já o tiver entregue, em vez de contar HEAD encaminhado como prova local. Essa escrita adicional troca banda por uma prova simples de persistência. A suíte deve confirmar que a versão escolhida do MinIO trata esse PUT no site alvo e que a cópia é recuperável com os peers isolados. Se a versão selecionada não oferecer essa garantia, essa etapa fica bloqueada para revisão técnica, sem enfraquecer o requisito.

Replicação e escrita explícita podem gerar versões S3 adicionais; recibos associam a versão física realmente gravada ao conteúdo lógico. Retenção de versões evita coleta de uma versão ainda referenciada. Não adicionar política de limpeza automática antes de cobrir essas referências.

### 5. Publicação e mudanças de configuração

Tabelas iniciais: users, sessions, folders, upload_operations, upload_parts, upload_part_copies, files, object_copies, cluster_nodes, cluster_configuration, manager_lease e cluster_events. IDs e timestamps não substituem a versão transacional da configuração.

Uma operação pending identifica usuário, pasta, nome, chave de idempotência, identidade imutável do conteúdo, partes, fase de transferência, configuração observada e cópias confirmadas. Pending engloba recebimento e confirmação, sem visibilidade no catálogo. Após obter todos os bytes e validar o arquivo completo, o worker obtém recibos de todos os sites dessa configuração. Em transação serializável, lê a configuração vigente, valida recibos para todos os membros não vazios e publica files/available. Se a configuração mudar, tenta novamente com o novo conjunto. A publicação não depende de o cliente manter a conexão.

Ao excluir um nó automaticamente, uploads pendentes podem ser reavaliados na nova configuração; não recebem sucesso só porque a espera expirou. Se a única origem dos bytes caiu, exigir recuperação ou reenvio. Após publicação, perda da resposta é resolvida consultando o ID da operação. Chave de idempotência é exclusiva por usuário; conteúdo diferente retorna conflito.

O coordenador de upload original não pode ser indispensável: workers saudáveis assumem operações persistidas com exclusão transacional e retomam de cópias disponíveis. Confirmações antigas são invalidadas quando a geração do storage muda, por exemplo após substituição do volume.

### 6. Admissão coordenada com publicações

O nó novo ou retornando sincroniza todas as versões publicadas, incluindo as publicadas durante a cópia. Antes de promover, o gerenciador e a transação de publicação disputam a mesma linha de configuração: validar que a geração de publicações conferida permanece atual, promover e incrementar a configuração atomicamente, ou repetir o trecho final da sincronização.

Isso fecha a corrida em que um upload seria publicado no último instante sem cópia no nó admitido. Objetos pending não entram no catálogo de recuperação; operações ainda em andamento observarão o conjunto expandido ao publicar. Nós recuperados não bloqueiam uploads enquanto estão syncing.

Adicionar nó CockroachDB significa iniciar armazenamento vazio com --join para um membro existente. Atualizar --join e reiniciar um nó preservando volume permite reencontrar o mesmo cluster; não funde clusters diferentes. Remoção permanente exige decommission e procedimento MinIO próprio. O painel dispara operações suportadas, não cria contêineres ou máquinas.

### 7. Interface e acesso

Acervo usa React com cadastro por login único e senha, sem exigir e-mail ou identidade externa. Sessões opacas persistidas no SQL permitem logout consistente e evitam estado local. Cookie HttpOnly e proteção das mutações contra CSRF; hash de senha e autorização por proprietário em todas as rotas, incluindo partes, retomada e cancelamento. Duplicidade de nome na mesma pasta retorna conflito, sem sobrescrita automática. O nome visual não exige renomear o módulo Go, banco, bucket ou serviços existentes.

A interface usa fundo claro, texto escuro e uma cor principal discreta, com tipografia e espaçamento consistentes. Navegação lateral, caminho da pasta, listagem central e área de transferências permanecem acessíveis ao navegar. Nome, símbolo e cores são próprios, sem elementos de marca do Google Drive. A área administrativa tem navegação separada e acesso restrito.

Cada arquivo possui sua operação e linha de progresso. Enviando mede bytes da tentativa atual e partes disponíveis; Confirmando armazenamento só começa quando os bytes completos estão preservados e validados; Concluído exige publicação confirmada. Se uma cópia temporária for perdida, a operação pode voltar a aguardar reenvio, explicando o ajuste de progresso. Preparação de integridade, espera por conexão, nova seleção necessária, erro e cancelamento têm mensagens próprias. A fila continua quando um arquivo falha e oferece repetição ou cancelamento por arquivo. Falhas transitórias têm tentativas espaçadas e limitadas por ciclo, com ação manual disponível após falhas persistentes.

Ao abrir a aplicação autenticada, consultar operações do proprietário no backend. Não depender de localStorage para descobrir uploads criados. Persistir localmente apenas a chave de criação por conta até resolver eventual resposta perdida; após logout, não exibir dados privados da conta anterior. Downloads usam a transferência nativa do navegador e resposta em fluxo, sem montar um Blob do arquivo completo em JavaScript. Aceitar tipos desconhecidos e arquivos vazios sem preview obrigatório; servir download como anexo.

Admin inicial é provisionado por configuração de desenvolvimento, sem senha embutida no frontend. O painel consome eventos e APIs do gerenciador, não executa loops de controle. A consulta administrativa de uploads usa uma resposta própria com identificador, estado, progresso, sites, cópias e erros sanitizados; não entrega nome, caminho, bytes, credenciais nem acesso aos endpoints de retomada ou cancelamento de outro usuário. O administrador seleciona qualquer nó e falha total ou parcial de backend, banco, storage ou comunicação de controle. Simulação desabilitada fora de modo dev; falhas simuladas rejeitam operações reais da camada selecionada e são rotuladas. Falha total suspende também o candidato a gerenciador, mas conserva um canal interno autenticado para desfazer a simulação. Desfazer a falha apenas inicia recuperação, não força ready. A API administrativa pode ser atendida por outro nó elegível. Matar processos, pausar contêineres e cortar rede pertencem ao roteiro externo de integração, sem montar o socket Docker no backend.

### 8. Ambiente e inicialização

Reusar Dockerfiles e scripts existentes. Corrigir dependências globais de inicialização: bootstrap frio forma banco e peers; partidas posteriores verificam estado persistido e deixam sobreviventes operar com quorum. Conferir peers e bucket nos três sites, não apenas retorno de replicate info. Não excluir volumes para resolver divergências silenciosamente.

O limite HTTP passa a proteger cada parte, incluindo overhead, sem impor teto ao arquivo total. O client_max_body_size atual de 100M pode comportar as partes iniciais; validar a configuração efetiva e remover a interpretação de limite de arquivo. Ajustar buffering e timeouts por requisição para não acumular arquivos inteiros nem repetir automaticamente mutações sem idempotência. Interface e API podem ser servidas pela entrada Nginx. Manter configurações por endpoints sem referências obrigatórias ao Railway.

O roteiro compara arquivos de 256 MiB e 2 GiB com a mesma configuração de partes e concorrência e registra memória de processo do navegador e backend, heap quando disponível, integridade e retomada. Não exigir arquivo físico maior como teste de rotina: testar os cálculos de tamanho, quantidade de partes e offsets acima de 2 GiB separadamente. Usar contadores de 64 bits no backend e validar a representação segura dos tamanhos na API. O arquivo de 2 GiB é referência de validação, não um máximo. Registrar falta de espaço ou recursos como verificação pendente, sem substituir o teste real por um teste de sintaxe.

### 9. Transporte em partes e identidade imutável

Usar File/Blob.slice e XMLHttpRequest com eventos de progresso no frontend. Default inicial de 32 MiB por parte e duas transferências de partes simultâneas na fila inteira; o arquivo de referência de 2 GiB possui 64 partes. Preparação, hashing e filas também têm concorrência limitada. Liberar buffers após uso. Esses valores limitam o conteúdo em processamento, não representam um teto exato para a memória total dos processos.

Antes de criar a operação, um worker do navegador percorre o arquivo por partes e produz um manifesto ordenado de tamanhos e SHA-256 por parte, além de SHA-256 incremental do arquivo completo. Nunca usar arrayBuffer, texto ou base64 para o arquivo inteiro, nem digest de arquivo inteiro que exija materializá-lo na memória. Selecionar a implementação de hash incremental na etapa correspondente e testá-la com vetores conhecidos. Mostrar preparação e seu progresso; isso acrescenta uma leitura local anterior ao envio, mas fixa a identidade necessária para impedir retomada com outro conteúdo.

A chave de idempotência é gerada antes da requisição de criação. O backend vincula usuário, destino, tamanho, manifesto e checksum completo à operação. O upload de cada posição valida tamanho e hash em fluxo antes de registrar recebimento. Repetições com os mesmos bytes retornam a mesma parte; conteúdo divergente é conflito. Metadados do cliente não bastam como prova: cada parte recebida e o arquivo montado são conferidos no backend. Arquivo vazio usa manifesto vazio e escrita final de objeto vazio, sem multipart vazio.

Guardar temporariamente cada parte como objeto imutável completo no MinIO local, identificado por operação, posição e hash. Registrar localização, versão e geração de storage no SQL. Isso permite que backends diferentes recebam partes da mesma operação, sem depender de afinidade no balanceador ou de um multipart aberto no site original. A replicação pode transportar esses objetos temporários, mas não se presume que ela preserve sessões multipart abertas. Uma resposta de parte recebida confirma apenas sua persistência local naquele momento.

Ao finalizar, workers leem as partes na ordem, verificam o checksum completo e escrevem o objeto final em fluxo, usando multipart local quando necessário, com buffers limitados também nos clientes S3. Os recibos finais continuam seguindo a decisão 4, incluindo escrita explícita e comprovação local em cada site. O ID de multipart de cada site é um detalhe de sua tentativa de montagem, separado do ID da operação. Interrupção dessa montagem pode exigir reiniciá-la a partir das partes, sem reenviar bytes do navegador que continuem recuperáveis. Não usar concatenação dos hashes das partes como substituto do SHA-256 do arquivo.

### 10. Retomada, falha e cancelamento

Com página aberta, consultar status e partes antes de repetir envios após falha de rede. Após reabertura, recuperar operações no SQL e pedir nova seleção quando faltarem bytes. O navegador não continuará enviando bytes locais enquanto estiver fechado. Nome e tamanho ajudam a sugerir associações de uma seleção múltipla, mas a interface recalcula o manifesto e checksum em memória limitada antes de retomar. Uma associação ambígua exige escolher a operação; conteúdo divergente é rejeitado sem alterar a original.

Workers assumem operações com concessão e geração persistidas. Escritas de progresso e finalização verificam essa geração para rejeitar o coordenador antigo. A verificação de elegibilidade da decisão 3 também vale para recebimento de partes e retomada: o upload não mantém o nó com falha autorizado a atender. Consultar a disponibilidade de partes antes de reutilizá-las, verificar os bytes ao ler e invalidar localizações de geração de storage substituída. Estado indisponível do controle deve aparecer como desconhecido/aguardando, sem presumir perda de todos os dados.

Apenas partes comprovadamente faltantes exigem reenvio. Partes recuperáveis via outro site podem ser usadas como origem de montagem; isso não as transforma em prova de cópia final local. Se todos os bytes necessários estiverem preservados, workers continuam a confirmação com navegador fechado. Se faltarem bytes, mantêm a operação pendente para recuperação ou nova seleção. A configuração vigente e o quorum continuam governando a publicação, independentemente do conjunto de sites que guardou partes temporárias.

Cancelar disputa a mesma operação em transação com a publicação: se cancelamento vencer, nenhuma finalização atrasada publica; se publicação vencer, responder com o arquivo concluído, sem removê-lo. O cancelamento é idempotente. Temporários só podem ser coletados depois de verificar que nenhuma operação ativa ou versão publicada os referencia. Nesta primeira versão, fechar a página ou uma queda transitória não expira operações automaticamente. Limpeza de temporários cancelados/concluídos é separada da retenção de versões publicadas e não pode destruir evidência de recibos.

### 11. Divisão da implementação

A política permanente está em docs/development-workflow.md. O coordenador mantém a
integração e libera dependências; a divisão abaixo não altera os contratos das specs.

| Frente | Escopo inicial | Liberação das etapas seguintes |
| --- | --- | --- |
| Base backend | 1.1 e contrato de configuração; depois 1.2 | Acesso SQL e migrações verificados liberam identidade e controle |
| Infraestrutura | 1.3, 1.4 e prova 1.5, em paralelo à base | Prova MinIO libera recibos; API de configuração do controle libera 3.1–3.2 |
| Controle | 2.1–2.5 | Contratos de cópias e publicação liberam recuperação 6.1–6.4 |
| Arquivos | 4.1–4.3 e 5.1–5.10 | Compartilha a configuração transacional com controle; integração conjunta em 5.5, 5.7 e 6.3 |
| Frontend | 7.1–7.2 e 7.8–7.11 após contratos HTTP; painel 7.3–7.7 após controle | Respostas simuladas permitem desenvolvimento local, mas conclusão depende das APIs reais |
| Integração | 8.1–8.6, conduzidas pelo coordenador com apoio das frentes | Executar cenários reais após integrar as dependências |

Na primeira rodada, apenas base backend e infraestrutura escrevem em paralelo.
Base backend possui backend/cmd e backend/internal/config; infraestrutura possui
Compose, scripts de inicialização e seu roteiro de verificação. O coordenador atribui
explicitamente novos pacotes e migrações quando iniciar a etapa SQL. Só um responsável
altera backend/go.mod e go.sum por vez. Nginx é atribuído à infraestrutura quando seu
contrato com o gerenciador estiver definido; frontend/package.json pertence à frente
frontend. O coordenador revisa specs, design e o tasks.md consolidado.

Contratos necessários antes de abrir frentes dependentes: configuração e identidade
local, esquema SQL e retry transacional, autorização de usuário, representação da
configuração vigente e da elegibilidade, recibos locais e API de operações/partes.
Os contratos devem ser concretizados no código da base antes de liberar consumidores.
Nenhum executor introduz uma segunda autoridade de publicação ou admissão.

## Risks / Trade-offs

- [Preparar e validar identidade exige leitura local] → mostrar progresso e executar hashing por partes fora da thread da interface; nova seleção após reabertura pode repetir essa leitura.
- [Temporários e montagem aumentam uso de disco] → informar falta de capacidade por operação e coletar apenas temporários sem referências; não prometer espaço ilimitado.
- [Retomada depende de bytes preservados ou do arquivo local] → reavaliar partes e solicitar reenvio quando a única cópia cair, sem enfraquecer a confirmação final.

- [Todos os sites obrigatórios precisam confirmar] → latência segue o site mais lento; falha confirmada pelo gerenciador reduz o conjunto automaticamente.
- [Exclusão reduz a redundância] → exibir número de cópias obrigatórias; nenhuma publicação sem site elegível e quorum SQL.
- [Partição ou perda de quorum] → rejeitar atendimento sem autoridade; não sacrificar consistência para simular autonomia.
- [Recibo não é uma transação distribuída com o disco] → cópia pode falhar depois de confirmada; detector e reparo tratam falhas posteriores, sem prometer sobrevivência à perda simultânea de todos os volumes.
- [MinIO encaminha consultas e replica de forma assíncrona] → escrita confirmada no alvo, recibo por site e teste com peers isolados.
- [Tags mutáveis e APIs admin] → registrar versões efetivamente exercitadas na integração; validar compatibilidade antes das etapas dependentes.
- [Nginx continua sendo entrada única no dev] → alta disponibilidade da entrada pública fica fora desta change; o gerenciador e o storage não dependem de uma instância única.

## Migration Plan

Implementar em etapas de tasks.md, mantendo migrações aditivas e volumes. Primeiro validar serviços externos e bootstrap, depois coordenação, publicação e interface. Não tratar os arquivos de planejamento como funcionalidade entregue. Rodar a suíte completa de falhas antes de declarar a change concluída.

Não há dados de aplicação existentes no código atual. Se houver volumes locais, inventariar antes de aplicar migrações ou alterar replicação. Rollback de binário só é permitido enquanto compatível com o esquema; não remover dados ou executar down -v automaticamente. Commit, deploy e execução de ambiente seguem pedidos separados do usuário.

## Referências técnicas

- CockroachDB, associação de nós e inicialização: https://www.cockroachlabs.com/docs/stable/start-a-local-cluster.html
- MinIO, site replication, proxy de GET/HEAD e replicação assíncrona: https://min.io/docs/minio/kubernetes/eks/operations/install-deploy-manage/multi-site-replication.html
- MinIO, versionamento de replicação: https://min.io/docs/minio/linux/administration/bucket-replication/bucket-replication-requirements.html
