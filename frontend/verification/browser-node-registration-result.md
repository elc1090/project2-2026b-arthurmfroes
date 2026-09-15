# Associação pelo formulário e retirada do quarto nó

Executado em 15/09/2026, entre 23:43 e 23:46 UTC, em `http://127.0.0.1:28100`.
A infraestrutura provisionou previamente o quarto backend, SQL associado ao cluster
e MinIO vazio. O frontend fez a associação exclusivamente pelo formulário do painel,
sem POST de registro anterior pelo coordenador.

A sessão administrativa nomeada foi `acervo-join-4`. Os campos enviados foram:

- Identidade `app-node-4`.
- Backend `http://app-node-4:8080`.
- Banco `postgresql://cockroach-4:26257/acervo_app_test`.
- Armazenamento `http://minio-4:9000`.
- Perfil de credenciais `default`, sem senhas nos endpoints.

O único POST de associação respondeu 202 às 23:44:14.933 UTC. Chave
`955153ca-91ad-4fca-9a72-e527a061b708`, operação
`03db424c-33ab-4810-bb01-ae85d35d0d3e`, nó
`59fd0a21-57a0-448b-b6da-7ce3dfff88a1`. A operação passou de preflight para storage
e apareceu complete às 23:44:25.260 UTC, sem erro. O nó só ficou Pronto depois da
sincronização, com transição registrada às 23:44:47.703 UTC e geração sincronizada 25.
O painel mostrou quatro nós prontos, sem botão de promoção manual.

## Leitura pelo proprietário no nó novo

A sessão separada `acervo-join-owner` entrou como proprietário da fixture `loss-65`.
O administrador não leu o conteúdo privado. Usando a sessão do proprietário,
GET `http://127.0.0.1:28104/api/folders` respondeu 200 e listou o arquivo publicado
`87792eb6-3d3b-4e1e-8911-e669c8a50e7a`, com 68157440 bytes.

O download nativo foi iniciado diretamente em
`http://127.0.0.1:28104/api/files/87792eb6-3d3b-4e1e-8911-e669c8a50e7a/download`.
Terminou sem erro, preservou o nome `loss-65.bin` e foi salvo em
`/tmp/acervo-join-4/download-node4-loss65.bin`. `sha256sum` coincidiu com a origem:

```text
25631f11bd18756ec0029380ec886af0c8824dc6b2706bbdb1d9451c7cf45f42
```

Esta prova confirma atendimento direto pelo novo backend após admissão. A persistência
física local de MinIO requer a inspeção com peers isolados, distinta desse download.
A sessão do proprietário foi fechada antes de retirar o nó.

## Retirada pelo painel

O formulário selecionou somente app-node-4. Solicitar retirada e Confirmar solicitação
de retirada enviaram um único POST, com resposta 202 às 23:45:50.565 UTC.

- Chave `dfa8babd-804a-4731-a97b-fc29505f4c58`.
- Operação `1cdb3c0d-70c2-467e-9514-fbfcbab695ee`.
- Mandato 12 e `last_error` nulo.

A interface acompanhou SQL, storage e complete. Às 23:46:23.780 UTC a operação
apareceu complete. A leitura final mostrou app-node-4 Retirado, os três demais
Pronto e configuração v54. Nenhum segundo POST ou chave nova foi criado durante
a espera. A confirmação externa de decommission SQL, peers MinIO e volumes
preservados cabe ao executor da infraestrutura.

## Evidências

`/tmp/acervo-join-4/events.json` registra todos os comandos e consultas administrativas.
Os status capturados foram 200 e 202. O console registrou somente o 401 esperado de
`/api/me` antes do login, sem exceção JavaScript. Screenshots `four-ready.png` e
`removed.png`, console e download ficam no mesmo diretório.

Ambas as sessões foram fechadas e a janela liberada antes das medições seguintes.
O executor frontend não alterou containers, redes ou volumes e não iniciou uploads.
Nenhum código da aplicação mudou nesta passagem.

## Conferência externa após retirada

O coordenador conferiu os relatórios da infraestrutura. Cockroach marcou o nó 4
como `decommissioned`, sem réplicas e sem atividade. Os três servidores MinIO
listaram somente minio-1, minio-2 e minio-3 na replicação. A operação de retirada
terminou em `complete`, sem erro; os três nós restantes estavam `ready`.

A consulta SQL também confirmou um único arquivo para a operação retomada de
65 MiB. Evidências: `/tmp/acervo-node4-ui-sql-retired.csv`,
`/tmp/acervo-node4-ui-retire-final.csv`, `/tmp/acervo-node4-ui-retire-operation.csv`
e `/tmp/acervo-node4-ui-minio-retired.json`. Os containers do quarto nó foram
parados após a conferência, com seus volumes preservados.
