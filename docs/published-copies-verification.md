# Prova física das cópias publicadas

Roteiro complementar de 8.5, sem novo upload: o script
`scripts/verify-published-copies.py` importa o harness vizinho para validar os dois
projetos de teste e registrar/restaurar cada parada. O modo sem flags é offline.

Com exclusividade de infraestrutura, executar pelo ambiente boto3 já existente:

```sh
/tmp/acervo-s3-proof-venv/bin/python scripts/verify-published-copies.py \
  --execute --file-id UUID_DO_ARQUIVO_PUBLICADO
```

O UUID é o de `files.id`, não o da operação. O roteiro para primeiro os três
backends, para impedir recuperações e novas versões durante o teste. Consulta
somente o banco `acervo_app_test`: exige três recibos de arquivo publicado,
associados à geração atual, com mesmo tamanho e SHA256. Registra UUID/nome do nó,
chave, versão física, tamanho, hash e gerações no relatório.

Para cada MinIO, para os outros dois, verifica o estado dos três containers e lê
a versão física exata diretamente do sobrevivente. Calcula SHA256 em blocos de
1MiB, sem acumular o arquivo na memória. Exige VersionId, Content-Length, tamanho
lido e hash iguais ao recibo SQL. Os backends permanecem parados nas três rodadas.

O finally restaura os MinIO antes dos backends. Se a restauração dos MinIO falhar,
não inicia os backends: preserva journal para recuperação. Não apaga objetos,
versões ou volumes. Não prova replicação durante publicação nem substitui testes
de recuperação: prova que os três recibos atuais têm bytes físicos legíveis sem
os peers no momento da execução.

```sh
/tmp/acervo-s3-proof-venv/bin/python scripts/verify-published-copies.py --recover
```

Arquivos padrão: `/tmp/acervo-published-copies-report.json` e
`/tmp/acervo-published-copies-journal.json`. Compartilha o lock do harness de falhas.

Entrega verificada somente com `py_compile` e modo offline, que imprimiu
`No services contacted.`. Integração real pendente; dependência boto3 exclusiva do
ambiente de testes, sem alteração das dependências da aplicação.
