# Prova complementar de perda de autoridade

`scripts/verify-authority-loss.py` reutiliza `Proof` do harness
`verify-distributed-failures.py` no mesmo diretório. A execução sem flags é
inteiramente offline. Não cria uploads, usuários, objetos ou volumes.

A execução real precisa de exclusividade nos projetos `acervo-app-test` e
`acervo-infra-runtime`, ambos previamente saudáveis. Preparar fora do repositório
um JSON com permissão 0600 contendo `cookie` (cabeçalho Cookie da sessão proprietária),
`file_id` (UUID do arquivo publicado) e `size` (tamanho exato em bytes).
Não registrar esse conteúdo em logs ou commits.

```sh
python3 scripts/verify-authority-loss.py --execute \
  --state /tmp/acervo-authority-private.json \
  --report /tmp/acervo-authority-loss-report.json \
  --journal /tmp/acervo-authority-loss-journal.json
```

Critérios, na ordem:

1. Dois downloads do arquivo existente, pelo LB e diretamente pelo backend1,
   retornam HTTP200 e Content-Length correto; cada um entrega 1MiB antes da falha.
2. O cliente continua lendo em blocos de 1MiB, com atraso de 250ms. Pausar somente
   Cockroach2 e Cockroach3, gravando intenção e identidade dos containers no journal.
3. Após 15s (lease configurado em 12s), novas requisições autenticadas `/api/me`
   retornam HTTP503 pelo LB e diretamente pelos três backends.
4. Ambos os downloads terminam com menos bytes que Content-Length por EOF ou erro
   de transporte dentro da janela de observação. Timeout do cliente, download
   completo ou fluxo ainda aberto não contam como sucesso. Falha antes da pausa
   é inconclusiva e não comprova cancelamento por perda de autoridade.
5. O finally fecha os clientes, restaura exclusivamente as ações registradas e
   espera os três nós prontos. Só então o relatório recebe `complete: true`.

O download direto exercita o guard periódico do backend. O download via LB pode
terminar também pelo encerramento do worker antigo do Nginx após revogação das
rotas; o resultado dele, isoladamente, não atribui a causa ao guard. Bytes já
armazenados em buffers de rede podem continuar chegando após o aborto remoto;
esta prova exige fluxo incompleto, não interrupção instantânea nem latência exata
medida no servidor. Não enfraquecer os critérios para acomodar falhas de preflight.

Em interrupção abrupta, recuperar antes de repetir:

```sh
python3 scripts/verify-authority-loss.py --recover \
  --journal /tmp/acervo-authority-loss-journal.json
```

Verificação desta entrega: `python3 -m py_compile` passou; execução sem flags
imprimiu `OFFLINE PLAN ... No services contacted.`. A prova real permanece pendente.

## Execução real em 15/09/2026

A prova passou com o arquivo de 256 MiB já publicado. Ambos os downloads tinham
HTTP 200 e bytes recebidos antes da pausa dos dois bancos. O fluxo pelo LB encerrou
com 33882112 bytes após 6,177 segundos; o direto, com 31402920 após 7,525 segundos.
Ambos terminaram por EOF incompleto, sem timeout do cliente. Após 15 segundos, as
quatro novas requisições responderam 503. Os três nós voltaram prontos e todas
as ações foram restauradas. Relatório `/tmp/acervo-authority-loss-report.json`,
`complete: true`. Os tempos incluem drenagem de buffers pelo cliente limitado.
