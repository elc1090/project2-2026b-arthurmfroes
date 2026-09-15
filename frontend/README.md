# Interface Acervo

React e TypeScript, com assets gerados por Vite. A entrada HTTP deve servir `dist/`
e encaminhar `/api/` ao backend na mesma origem. A autenticação usa o cookie
HttpOnly do backend; o frontend não armazena senha nem token de sessão.

```sh
cd frontend
npm ci
npm test
npm run build
```

`npm run dev` existe para desenvolvimento isolado da interface. O ambiente completo
usa a entrada Nginx; configurar o encaminhamento de `/api/` é necessário se usar Vite
separadamente. A interface não incorpora nomes de serviços, portas ou provedores.

## Transferências

Um worker prepara um arquivo por vez usando `@noble/hashes`, SHA-256 incremental e
leituras de até 32 MiB. A fila compartilha dois slots de envio com XMLHttpRequest;
cada requisição recebe uma fatia Blob. O download usa link nativo, sem acumular os
bytes em JavaScript. Não há filtro de tipo nem limite total baseado nos 2 GiB de teste.

O backend fornece a fila ao entrar novamente. Para operações incompletas, a seleção
múltipla associa nome e tamanho quando há uma única candidata, depois compara o hash
completo e o manifesto. Associações ambíguas exigem selecionar a linha desejada.
Disponibilidade desconhecida aguarda nova consulta. Bytes enviados só concluem a
operação quando o backend retorna `available`.

As chaves de criação são persistidas por conta antes do POST e removidas após obter
uma operação. A API deve incluir `idempotency_key` nos resultados privados. Nenhum
nome, manifesto ou conteúdo é persistido pelo frontend em localStorage. Ao sair,
requisições e workers são interrompidos e a fila da conta deixa a memória da interface.

## Verificação executada

Os testes Node verificam SHA-256 conhecido, vazio, parte final menor, identidade
divergente, offsets acima de 2 GiB, leituras limitadas e concorrência global. Os testes
da fila usam respostas simuladas para recuperação por conta, resposta de criação
perdida, espera por disponibilidade desconhecida e confirmação após publicação.
Também verificam erro operacional, retry durante encerramento da tentativa anterior,
cancelamento com resposta atrasada, logout durante consultas e associações ambíguas.

`npm run build` verifica TypeScript e gera o worker em arquivo separado.

Ainda dependem da integração: navegador real, entrada Nginx, retomada com falhas dos
serviços, memória dos processos em transferências de 256 MiB e 2 GiB e os cenários
reais dos controles administrativos. O painel consulta `/api/admin/cluster` a cada cinco segundos e
mostra estados, componentes, concessão, eventos e recibos por site. Observações
ausentes ou com mais de quinze segundos são identificadas; isso não altera a
decisão de elegibilidade do gerenciador. O painel não apresenta dados simulados. Estes testes locais não comprovam tolerância distribuída.


## Simulação administrativa

Quando a API informa `simulation_enabled`, cada nó oferece seleção de backend,
banco, storage, comunicação ou falha total. O nó gerenciador também pode ser
selecionado. `Restaurar nó` envia `mode: none`; saúde e admissão continuam sendo
observadas na API, sem promoção local para `ready`.

O resultado da solicitação aparece separado do estado observado. Se a resposta
se perde, a interface permite repetir exatamente a mesma ação. As chamadas têm
timeout e são interrompidas ao sair da área administrativa. Os testes locais
verificam o contrato do POST e seus erros; a detecção real exige o teste integrado
no navegador e backend.

## Associação e retirada

O formulário administrativo registra nós já provisionados com endpoints genéricos
e perfil de credenciais `default`. A retirada é assíncrona e exibe verificações,
etapas SQL/storage, mandato e impedimentos. As etapas não alteram o estado visual
do nó: a admissão continua vindo da observação do gerenciador.

Comandos sem resposta confirmada ficam em localStorage separados por conta, com
chave e payload para repetição idempotente após reabrir. Não há senha de serviço no
formulário; endpoints contendo userinfo ou query são rejeitados antes dessa persistência.
Leitura bloqueada ou conteúdo local malformado não derruba o painel. Se a gravação
da chave falhar, o comando não é enviado.
A API pode incluir a chave na listagem para resolver a pendência automaticamente.

Apenas intenção de retirada em `preflight` pode ser cancelada pela interface.
Etapas SQL/storage não oferecem cancelamento. Bloqueio de topologia identifica a
necessidade de destinos para três réplicas SQL, além de simplesmente ter nós saudáveis.
Os testes dessas ações usam respostas simuladas; associação/retirada reais pelo
painel ainda dependem da integração do contrato no ambiente de teste.

Recuperação automática de armazenamento aparece como operação `replace`, com
etapa `remove_old` para retirar a associação antiga. Ela não oferece comando
manual nem cancelamento no painel.
