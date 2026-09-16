## ADDED Requirements

### Requirement: Exclusão lógica atômica e limpeza física distribuída
A exclusão permanente SHALL tornar o arquivo indisponível por uma decisão persistida antes de responder sucesso, sem aguardar que todos os storages estejam alcançáveis. O sistema SHALL remover depois cada versão física registrada em seu site e SHALL retomar essa limpeza após falhas. Arquivos excluídos SHALL ficar fora dos planos de recuperação e uma admissão SHALL não restaurar nem conservar como ativa uma cópia pendente de exclusão.

#### Scenario: Storage indisponível durante a exclusão
- **WHEN** o proprietário exclui um arquivo enquanto um dos storages está indisponível
- **THEN** o arquivo deixa de ser acessível e a limpeza da cópia pendente continua quando o site voltar

#### Scenario: Nó retorna com uma cópia excluída
- **WHEN** um nó em recuperação ainda possui uma versão física de arquivo excluído
- **THEN** essa versão é removida antes da readmissão sem recriar o arquivo no catálogo

#### Scenario: Exclusão concorrente com novo download
- **WHEN** a exclusão confirma antes de uma nova solicitação de download obter os metadados publicados
- **THEN** o novo download é rejeitado

#### Scenario: Download já iniciado
- **WHEN** a exclusão confirma depois que um download autorizado começou a transmitir bytes
- **THEN** esse fluxo pode terminar, mas nenhuma nova solicitação de download é admitida
