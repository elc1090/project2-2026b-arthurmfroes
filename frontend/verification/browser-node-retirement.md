# Associação observada e retirada do quarto nó pelo painel

Em 15/09/2026, sessão Chromium headless `acervo-topology-ui`, entrada real
`http://127.0.0.1:28100`, conta administrativa acadêmica.

A primeira etapa foi somente leitura. O coordenador associou o quarto nó pela API
e substituiu seu storage em uma rodada separada. O painel exibiu quatro nós prontos
e as operações já concluídas:

| Operação | Tipo exibido | Mandato |
| --- | --- | --- |
| 91153af7-e161-4d74-b22f-8f8ab03052cc | Associação | 28 |
| 9cd07948-f45d-45ce-af91-42778157cc6b | Recuperação de armazenamento | 31 |

Esta observação não substitui as provas de associação e bytes do coordenador.
O login inicial recebeu 503 durante a mudança de topologia. A interface mostrou
indisponibilidade temporária; a repetição permitiu acessar o painel.

## Retirada executada pela interface

Após liberação específica, foi selecionado somente `app-node-4` no formulário,
seguido de `Solicitar retirada` e `Confirmar solicitação de retirada`.
Foi enviado exatamente um POST de retirada:

- Nó: `fdcb17a5-fb58-4726-a9dd-5ad0f1665043`.
- Chave: `6563bb31-b796-4985-873e-cf12d85a3d5f`.
- Operação: `9b8bec55-430d-41f6-b09a-4036af9fdb51`.
- Resposta: HTTP 202, `retire/preflight`, mandato 32, sem erro.

O painel avançou para `Configurando banco` e marcou o nó indisponível, mantendo
os outros três prontos. A primeira espera automatizada de 30 segundos expirou;
nenhuma chave nova ou segundo POST foi criado. A leitura posterior mostrou:

- Retirada com `Etapas concluídas`, mandato 34 e sem impedimento.
- `app-node-4` em `Retirado`, nós 1, 2 e 3 em `Pronto`.
- Configuração v83, geração de publicações 26.
- Gerenciador `app-node-1`, mandato 34; antes do comando era `app-node-3`, mandato 32.
- Nenhum comando local sem resposta confirmada. O seletor de retirada deixou de
  oferecer o nó 4 e manteve somente os três restantes.

O console registrou 19 falhas HTTP 503 acumuladas de consultas me/login/cluster/
node-operations durante essa sessão; não registrou exceções JavaScript. A interface
preservou os dados anteriores, identificou falha de atualização e depois exibiu os
resultados recuperados. O relatório não apresenta esses intervalos como ausência
de indisponibilidade.

Imagens locais: `/tmp/acervo-topology-operations.png` e
`/tmp/acervo-node4-retired.png`. Log da sessão:
`/tmp/acervo-frontend/.playwright-cli/console-2026-09-15T20-16-44-504Z.log`.

O executor do frontend não executou SQL, Docker, exclusão de volumes ou falhas nessa
rodada. A confirmação nativa de decommission SQL, peers MinIO e volumes preservados
fica na evidência do coordenador. Nenhum outro nó foi retirado e nenhum upload de
256 MiB ou 2 GiB foi iniciado. A sessão própria foi fechada ao concluir.
