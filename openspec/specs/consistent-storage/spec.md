## Purpose

Garantir que arquivos confirmados tenham cópias verificadas em todos os sites obrigatórios e apresentem o mesmo estado lógico em todos os nós elegíveis.

## Requirements

### Requirement: Confirmação de todas as cópias obrigatórias
O sistema SHALL retornar sucesso final de upload somente após comprovar persistência da mesma versão imutável, tamanho e checksum em cada site obrigatório da configuração usada na publicação. A confirmação SHALL comprovar armazenamento no site alvo, sem aceitar resposta obtida por proxy de outro site como prova local. O conjunto obrigatório SHALL ser não vazio. Confirmação de recebimento de uma parte SHALL não significar confirmação do arquivo nem prometer que a parte sobrevive à perda de seu único site.

#### Scenario: Uma confirmação atrasada
- **WHEN** dois sites confirmam e o terceiro ainda não possui o conteúdo
- **THEN** o upload continua pendente e não retorna sucesso

#### Scenario: Resposta de outro site
- **WHEN** um site sem cópia local responde a uma consulta encaminhando-a para um peer
- **THEN** essa resposta não é contada como confirmação de persistência local

#### Scenario: Conteúdo divergente
- **WHEN** um site contém objeto de mesmo nome com versão ou checksum diferente
- **THEN** sua confirmação é rejeitada e o arquivo não é publicado nessa configuração

### Requirement: Publicação atômica com configuração vigente
O sistema SHALL publicar metadados apenas quando todas as confirmações correspondem à configuração vigente. Uma alteração de membership durante upload SHALL exigir revalidação antes da publicação. A exclusão automática de um site SHALL permitir conclusão com todos os sites restantes, desde que a exclusão esteja confirmada e o conteúdo esteja preservado neles.

#### Scenario: Site falha durante upload
- **WHEN** o nó 3 falha e o gerenciador confirma uma nova configuração com nós 1 e 2
- **THEN** a operação pode concluir após validar cópias nos nós 1 e 2 na nova configuração, sem esperar decisão humana

#### Scenario: Nó admitido antes da publicação
- **WHEN** um novo nó torna-se obrigatório antes de uma operação ser publicada
- **THEN** essa operação também exige a confirmação do novo nó

### Requirement: Operações idempotentes e retomáveis
O sistema SHALL associar cada upload a uma chave de idempotência do usuário e a conteúdo imutável. Repetição com o mesmo conteúdo SHALL retomar ou retornar o mesmo resultado; reutilização para outro conteúdo SHALL retornar conflito. O cliente SHALL preservar a chave antes da criação para resolver uma resposta perdida; uma operação criada SHALL ser consultável por seu identificador ou pela chave do proprietário. Quando for possível responder a um timeout, a API SHALL informar o identificador conhecido e não afirmar sucesso não confirmado. Falha de rede sem resposta SHALL não exigir criação de outra operação.

#### Scenario: Resposta perdida
- **WHEN** a publicação é confirmada no banco mas a resposta ao cliente se perde
- **THEN** consultar ou repetir a operação retorna o arquivo já publicado sem duplicação

#### Scenario: Origem cai antes da replicação
- **WHEN** o único site com os bytes falha antes de copiá-los
- **THEN** a operação permanece sem sucesso e exige recuperação da cópia ou reenvio idempotente pelo cliente

#### Scenario: Resposta de criação perdida
- **WHEN** a operação foi criada mas o cliente não recebeu seu identificador
- **THEN** repetir a criação com a mesma chave e conteúdo recupera a mesma operação

### Requirement: Partes persistidas e recuperáveis
O sistema SHALL registrar partes por operação, posição, tamanho, checksum e localização recuperável, sem depender da memória do coordenador original. Reenvio de parte idêntica SHALL ser idempotente; bytes divergentes SHALL ser rejeitados sem substituir a parte válida. Uma consulta de retomada SHALL distinguir partes disponíveis, faltantes e disponibilidade ainda desconhecida, sem tratar um registro antigo como prova de bytes preservados. Somente conteúdo completo, ordenado e validado SHALL avançar para confirmação e publicação.

#### Scenario: Coordenador indisponível
- **WHEN** o nó que coordenava o upload falha e outro nó elegível assume com acesso ao estado compartilhado
- **THEN** o sucessor aproveita partes recuperáveis, solicita reenvio das faltantes e mantém a mesma operação

#### Scenario: Parte perdida com o storage
- **WHEN** uma parte recebida existia somente em um site que ficou inacessível
- **THEN** o sistema exige recuperação ou reenvio dessa parte, sem apagar o progresso das outras nem declarar sucesso final

#### Scenario: Parte repetida ou divergente
- **WHEN** o cliente repete uma posição já recebida
- **THEN** bytes idênticos mantêm o resultado e bytes divergentes geram conflito sem alterar o conteúdo válido

### Requirement: Identidade do conteúdo na retomada
O sistema SHALL vincular a operação ao conteúdo original e verificar o arquivo selecionado novamente antes de combinar seus bytes com partes preservadas. Nome, tamanho e data de modificação SHALL não ser suficientes para comprovar identidade. A validação SHALL processar o conteúdo com memória limitada e conferir a integridade do arquivo completo antes da publicação.

#### Scenario: Mesmo nome e tamanho com conteúdo diferente
- **WHEN** o usuário seleciona para retomada um arquivo de mesmo nome e tamanho, mas com bytes diferentes
- **THEN** a retomada é rejeitada para essa operação, sem misturar arquivos ou descartar as partes originais válidas

### Requirement: Confirmação independente do navegador e cancelamento
Com todos os bytes necessários preservados e autoridade disponível, o backend SHALL continuar a confirmação e publicação mesmo com o navegador fechado. Cancelamento e publicação SHALL ser mutuamente exclusivos na decisão persistida. Operação cancelada SHALL rejeitar novos envios e finalizações atrasadas. Remoção de temporários SHALL preservar versões publicadas ou ainda referenciadas por operações ativas.

#### Scenario: Navegador fechado
- **WHEN** o navegador fecha enquanto uma operação com bytes completos aguarda cópias
- **THEN** um worker continua o processamento e persiste o resultado para consulta posterior

#### Scenario: Finalização depois do cancelamento
- **WHEN** um worker tenta finalizar uma operação cujo cancelamento já foi confirmado
- **THEN** não publica arquivo nem torna a operação ativa novamente

### Requirement: Integridade e recuperação de publicados
O sistema SHALL usar apenas versões publicadas em downloads e verificar as cópias necessárias à recuperação de um nó. Uma cópia ausente ou corrompida SHALL impedir a admissão desse nó até reparo.

#### Scenario: Download após falha de site
- **WHEN** um upload já confirmado perde um site e outro nó permanece elegível
- **THEN** esse nó entrega a mesma versão e os mesmos bytes publicados

#### Scenario: Corrupção durante ausência
- **WHEN** um site retorna com checksum divergente para arquivo publicado
- **THEN** o nó permanece em recuperação até obter e verificar uma cópia correta

### Requirement: Réplicas físicas independentes
Cada nó SHALL possuir object storage e volume próprios, cooperando por replicação. A indisponibilidade de um site SHALL não tornar os demais sites indisponíveis por dependência de armazenamento central.

#### Scenario: Storage local indisponível
- **WHEN** o processo e volume de minio-1 ficam inacessíveis
- **THEN** minio-2 e minio-3 continuam capazes de armazenar e fornecer seus objetos confirmados

### Requirement: Exclusão lógica atômica e limpeza física distribuída
A exclusão permanente SHALL tornar o arquivo indisponível por uma decisão persistida antes de responder sucesso, sem aguardar que todos os storages estejam alcançáveis. O sistema SHALL remover depois todas as versões físicas da chave final canônica em cada site registrado, inclusive versões criadas antes de uma falha que impediu a persistência do recibo, e SHALL retomar essa limpeza após falhas. Arquivos excluídos SHALL ficar fora dos planos de recuperação e uma admissão SHALL não restaurar nem conservar como ativa uma cópia pendente de exclusão.

#### Scenario: Storage indisponível durante a exclusão
- **WHEN** o proprietário exclui um arquivo enquanto um dos storages está indisponível
- **THEN** o arquivo deixa de ser acessível e a limpeza da cópia pendente continua quando o site voltar

#### Scenario: Nó retorna com uma cópia excluída
- **WHEN** um nó em recuperação ainda possui uma versão física de arquivo excluído
- **THEN** essa versão é removida antes da readmissão sem recriar o arquivo no catálogo

#### Scenario: Versão final órfã de uma tentativa anterior
- **WHEN** a chave final possui uma versão física que não corresponde ao recibo SQL atual
- **THEN** a limpeza enumera a chave exata e remove também essa versão antes de concluir para o site

#### Scenario: Exclusão concorrente com novo download
- **WHEN** a exclusão confirma antes de uma nova solicitação de download obter os metadados publicados
- **THEN** o novo download é rejeitado

#### Scenario: Download já iniciado
- **WHEN** a exclusão confirma depois que um download autorizado começou a transmitir bytes
- **THEN** esse fluxo pode terminar, mas nenhuma nova solicitação de download é admitida
