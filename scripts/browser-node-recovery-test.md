# Roteiro de reabertura com worker parado e parte perdida

Este helper prepara somente o intervalo entre duas sessões do navegador. Não envia arquivo nem conclui a prova 8.4 sozinho. Usa os projetos isolados existentes e a autorização específica para apagar versões da parte 1 da operação fornecida. Não serve como política da aplicação nem como rotina de produção.

1. Na primeira sessão, criar arquivo de 65 MiB: partes 0 e 1 de 32 MiB, parte 2 de 1 MiB. Confirmar recebimento de 0/1 e impedir envio de 2. Fechar a sessão antes de executar o helper. Preservar a fila IndexedDB e o arquivo original para a segunda sessão.
2. Exportar apenas o cookie da conta desse teste para arquivo `0600`, no formato `nome=valor`. Registrar o UUID da operação. Não publicar cookie, credenciais ou arquivo de sessão.
3. Após liberação da janela exclusiva, executar da raiz:

```sh
python3 scripts/verify-browser-node-recovery.py --execute \
  --operation UUID_DA_OPERACAO --cookie-file /tmp/acervo-browser-cookie.txt \
  --journal /tmp/acervo-browser-node-journal.json \
  --report /tmp/acervo-browser-node-report.json
```

O helper valida manifesto e estado pela API, exige ausência de recibo SQL da parte 2 e observa holder com lease ativo. Usa journal antes de parar o container desse backend. Lista e apaga somente versões da chave exata `parts/<op>/1/<sha256>` nos três sites; cada versão recebe uma entrada em `.versions.json` antes do DELETE com `versionId`. Não modifica recibos, gerações SQL ou objetos da parte 0. Não usa DELETE sem versão nem apaga buckets/volumes.

Antes de entregar ao navegador, exige GET autoritativo com parte 0 `available`, parte 1 `missing` e parte 2 `missing`. `unknown`, timeout ou falta de resposta não provam perda. Após sucesso, deixa o backend identificado parado e a operação pendente para a segunda sessão. O relatório continua sem `complete=true`: o navegador ainda precisa provar o restante.

4. Na segunda sessão, verificar pedido de reseleção do arquivo. Escolher o mesmo arquivo e registrar requisições de partes: 1 e 2 devem ser enviadas; 0 deve ser preservada. Aguardar publicação e validar download/tamanho/SHA-256. Um arquivo diferente deve ser recusado sem alterar a operação, caso essa verificação faça parte da sessão coordenada.
5. Restaurar o backend e verificar três nós prontos:

```sh
python3 scripts/verify-browser-node-recovery.py --recover \
  --journal /tmp/acervo-browser-node-journal.json \
  --report /tmp/acervo-browser-node-report.json
```

`--recover` restaura apenas ações de containers do journal após validar identidade e exige 6 GiB livres. Não desfaz deleções: bytes perdidos serão repostos exclusivamente pelo reenvio do navegador. Não deve ser confundido com restaurar versões anteriores.

O helper exige 7,5 GiB iniciais, monitora entre etapas de remoção o piso de 6 GiB e crescimento máximo de 1,5 GiB. Em falha, tenta cancelar somente a operação fornecida; se confirmar término e houver orçamento, restaura containers. Sem confirmação, mantém o nó parado e pausa os demais backends com journal para intervenção do coordenador. Não atua em bancos ou MinIO. Durante a segunda sessão, o coordenador continua responsável por observar capacidade; o helper já terminou.

Preparação offline:

```sh
python3 scripts/verify-browser-node-recovery.py
python3 scripts/verify-browser-node-recovery.py --self-test
```

O auto-teste valida seleção da chave e estados autoritativos. O caminho real Docker/S3/SQL ainda não foi executado. O helper carrega os dois harnesses irmãos; ao rodar da worktree, usar `--harness-dir /caminho/da/raiz/scripts` para carregar as versões integradas.
