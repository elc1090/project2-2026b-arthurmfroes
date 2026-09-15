# Medição de memória do navegador e dos backends

Procedimento preparado para a tarefa 8.5. A criação de arquivos e a execução de
transferências abaixo dependem da liberação do coordenador. Nenhuma transferência
foi feita durante a preparação deste documento.

## Condições iguais

Executar 256 MiB e, quando houver orçamento confirmado, 2 GiB. Usar os mesmos
32 MiB por parte, dois XHR simultâneos na fila e um worker de hashing. Cada arquivo
é enviado sozinho, sem outros uploads da sessão. São 8 e 64 partes respectivamente.
Abrir uma sessão nova para cada tamanho, com a mesma versão de Chromium, viewport,
interface, backends e configurações; preservar os volumes existentes.

O espaço necessário inclui partes, objetos finais, versões replicadas e downloads.
A estimativa inicial de 24 GiB cobria partes e finais. Incluindo as cópias locais do
download, o orçamento pode chegar a 28 GiB antes da margem. O
[plano revisado](e2e-remaining-plan.md) reserva cerca de 36 GiB livres; novas versões
finais podem exigir mais. Os aproximadamente 22 GiB livres observados pelo
coordenador não liberam esse teste. Registrar como pendente se o orçamento continuar
insuficiente. Sparse file economiza apenas a origem; o armazenamento distribuído
continua recebendo todos os bytes.

## Preparação do arquivo e integridade

Depois da liberação, usar um diretório exclusivo de cada rodada:

```sh
mkdir -p /tmp/acervo-memory-256
truncate -s 268435456 /tmp/acervo-memory-256/reference-256.bin
sha256sum /tmp/acervo-memory-256/reference-256.bin
```

Para 2 GiB, somente depois de orçamento aprovado: `truncate -s 2147483648` em outro
arquivo e diretório. `sha256sum` lê em fluxo. Não produzir ArrayBuffer/Blob do arquivo
inteiro dentro de JavaScript para gerar o fixture.

## Sessão exclusiva e processos

Seguir a skill browser, abrir com nome `acervo-memory-256` ou `acervo-memory-2048` e
manter o dashboard compartilhado em `http://localhost:9323`. Não usar sessão default
nem perfil pessoal. Não iniciar servidor de desenvolvimento.

O PID informado por `playwright-cli open` pode ser do daemon. Antes de medir,
inspecionar `/proc/<pid>/cmdline` e a árvore de processos para encontrar o processo
raiz Chromium desta sessão. Confirmar seu `--user-data-dir` e relação com a sessão;
não escolher um Chrome de outra tarefa. Fornecer esse PID ao helper, que rejeita
um renderer ou daemon e verifica `start_ticks` para não seguir PID reutilizado.

O helper acompanha somente descendentes dessa raiz, incluindo renderers e utility
processes. Dedicated workers podem executar dentro do processo do renderer: seus
bytes estão incluídos, mas não são separados artificialmente como outro processo.
Processos reparentados fora da árvore não são atribuídos automaticamente à sessão.

## Amostragem

`sample-memory.py` usa apenas biblioteca padrão Python e leituras do Linux. Lê
`smaps_rollup` de cada processo, somando RSS e PSS, e `memory.current`/`memory.peak`
do cgroup v2 de cada backend. Não reseta contadores, não sinaliza Chrome, não reinicia
containers nem modifica limites. Precisa de leitura permitida nesses arquivos.
Falha explícita se o cgroup v2 não estiver acessível; não substituir por estimativa.

Após identificar os nomes reais dos três containers e o PID exclusivo, o comando é:

```sh
printf 'baseline\n' > /tmp/acervo-memory-256/phase
python3 frontend/verification/sample-memory.py \
  --browser-pid PID_CHROMIUM_DA_SESSAO \
  --container NOME_BACKEND_1 --container NOME_BACKEND_2 --container NOME_BACKEND_3 \
  --phase-file /tmp/acervo-memory-256/phase \
  --stop-file /tmp/acervo-memory-256/stop \
  --output /tmp/acervo-memory-256/samples.jsonl --interval 0.5
```

Executar o sampler em processo separado e guardar seu identificador, sem encadear
espera bloqueante longa no agente. Registrar pelo menos cinco segundos de baseline
após login/listagem estabilizados. Gravar `hashing` no arquivo de fase antes da
seleção, `upload` quando a linha entrar em Enviando e `confirming` quando todos os
bytes tiverem sido enviados. Usar o texto/estado visível da linha com Playwright,
registrando os instantes das transições no relatório. Não alterar o código de hash
nem extrair seus buffers para instrumentar.

O watcher de fase pode consultar `.transfer .status` em intervalos curtos pelo
Playwright CLI da sessão nomeada. Se uma fase for curta e não observada, registrar
explicitamente; não inventar máximos para a fase. A confirmação final pode continuar
após falhas de consulta, por isso não parar no progresso de bytes igual a 100%.

Após `Concluído`, registrar cinco segundos de `completed`, criar o arquivo `stop`
e aguardar o resumo. O sampler também finaliza por SIGINT/SIGTERM enviados apenas
ao seu processo ou pelo limite de duração. O Chrome permanece intacto.

## Interpretação e download

- RSS agregado conta páginas compartilhadas em cada processo; PSS divide essas
  páginas e é mais apropriado para comparar o conjunto da sessão.
- Máximos de RSS, PSS e `memory.current` são **máximos amostrados**, a cada 500 ms,
  não picos contínuos. Amostras com processo inacessível são marcadas incompletas.
- `memory.peak` é o pico do cgroup desde sua criação/reset anterior, incluindo carga
  anterior. O baseline registra esse valor; ele não vira pico exclusivo da fase.
- Memória do backend no cgroup inclui cache de arquivos e componentes do container,
  não somente heap Go. Não comparar isso com heap JavaScript como se fossem iguais.

Parar a medição do upload antes do download. Usar link nativo e salvar o download
com Playwright, depois executar `sha256sum` no arquivo salvo. Não usar fetch seguido
de arrayBuffer/blob para o download inteiro. O coordenador verifica ainda as três
versões finais por site e seus recibos com S3 em fluxo; registrar os resultados no
mesmo relatório. Leitura por proxy não comprova persistência local.

Guardar `samples.jsonl`, `samples.summary.json`, tamanhos, hashes, versões, parâmetros,
tempos, screenshots e limitações. Comparar baseline e máximos por fase dos dois
arquivos somente quando os dois testes reais tiverem sido executados.


Em 15/09/2026, após o usuário liberar espaço, o coordenador confirmou cerca de
124 GiB livres no filesystem dos volumes e artefatos. A restrição de disco foi
resolvida; a medição de 2 GiB está autorizada para uma janela exclusiva posterior
às provas de recuperação. Isso ainda não constitui execução do teste.
