# Entrada do Acervo

O reconciliador usa Python e sua biblioteca padrão para consultar o controle HTTP,
validar JSON, gerar configuração e supervisionar o Nginx. Não precisa de pacotes
Python externos. O processo encerra o Nginx se não conseguir revogar uma rota expirada.

Configuração por ambiente:

| Variável | Uso | Padrão |
| --- | --- | --- |
| CONTROL_ENDPOINTS | URLs HTTP/HTTPS dos backends de controle, separadas por espaço | Obrigatória |
| CONTROL_TOKEN | Credencial Bearer compartilhada com os backends | Obrigatória |
| CONTROL_INTERVAL | Intervalo entre consultas, em segundos | 0.5 |
| CONTROL_TIMEOUT | Prazo de espera por endpoint, em segundos | 1 |
| CONTROL_MAX_STALE | Prazo máximo de conservação do snapshot, em segundos | 3 |

O controle entrega `configuration.Version`, `configuration.Members` e
`valid_for_ms`. O prazo efetivo usa o menor prazo entre a configuração local e a
lease informada pelo SQL, descontando o tempo da consulta. Uma versão menor ou uma
composição diferente com a mesma versão é rejeitada. Uma resposta nova da mesma
versão pode renovar sua validade. A consulta tenta outros endpoints quando um falha;
o último endpoint válido passa a ser tentado primeiro.

Somente membros `ready` entram na configuração. `split_clients` distribui as
requisições entre suas URLs, inclusive uma mistura de HTTP e HTTPS. Para HTTPS, o
Nginx verifica o certificado pela CA em `/etc/ssl/cert.pem` e usa o hostname do
endpoint em SNI. Um ambiente com CA privada deve fornecer seu bundle confiável nesse
caminho. URLs não podem conter credenciais, caminhos, query strings ou fragmentos.

Cada requisição escolhe um backend; não há repetição automática em outro servidor.
A retirada pelo gerenciador e a retomada idempotente pelo cliente tratam falhas.
Configuração sem membros ou sem autoridade válida devolve 503 para `/api` e
`/health`. `/internal` não é acessível pela entrada pública. Os demais caminhos usam
os arquivos de `/usr/share/nginx/html`, com fallback para `index.html`.

O access log omite somente respostas 2xx de `GET /api/admin/cluster` e
`GET /api/admin/node-operations`, incluindo consultas com query string. Erros nesses
endpoints, outros métodos e todo o restante do tráfego continuam registrados.

A configuração candidata passa por `nginx -t` antes da troca por rename e reload.
Falha de validação conserva a anterior até expirar; falha ao sinalizar reload restaura
o arquivo anterior. Workers antigos ainda podem finalizar conexões durante a troca:
as mutações continuam exigindo o Guard transacional do backend. O limite 100M é por
requisição/parte, não pelo tamanho total do arquivo; buffering de upload está desligado.

Verificação:

```sh
python3 -m unittest discover -s nginx -p 'test_*.py' -v
docker build -f nginx/Dockerfile -t acervo-lb-control-test:local .
ACERVO_NGINX_TEST_IMAGE=acervo-lb-control-test:local python3 -m unittest discover -s nginx -p 'test_*.py' -v
```

O teste Docker cria e remove apenas seus próprios containers e sua rede. Exercita
DNS por hostname Docker, TLS com CA de teste e rejeição de nome incorreto, mistura
HTTP/HTTPS, fallback do controle, zero membros, expiração, SPA e bloqueio interno.
Também confirma no access log real que as duas consultas periódicas bem-sucedidas
são omitidas enquanto erros, mutações e tráfego comum permanecem. Falhas de
validação/reload são verificadas separadamente com executor simulado.
