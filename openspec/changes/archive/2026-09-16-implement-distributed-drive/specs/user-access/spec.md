## Purpose

Permitir cadastro e acesso privado aos arquivos, mantendo a mesma identidade e autorização em todos os backends elegíveis.

## ADDED Requirements

### Requirement: Cadastro e autenticação compartilhados
O sistema SHALL cadastrar usuários com login único e senha, sem exigir e-mail ou provedor externo de identidade, e autenticar suas credenciais sem depender da memória de uma réplica específica. Credenciais incorretas SHALL ser rejeitadas e senhas SHALL ser armazenadas por hash apropriado para senhas.

#### Scenario: Sessão entre réplicas
- **WHEN** um usuário autentica no nó 1 e sua próxima requisição chega ao nó 2
- **THEN** a mesma sessão válida é reconhecida sem novo login

#### Scenario: Cadastro concorrente
- **WHEN** dois nós recebem cadastro para a mesma identidade
- **THEN** apenas uma conta é criada e a outra tentativa recebe conflito

### Requirement: Isolamento dos dados do usuário
O sistema SHALL autorizar toda operação sobre pastas, arquivos e uploads pelo proprietário autenticado, inclusive acesso por identificador direto, listagem de transferências, consulta de partes, retomada, envio e cancelamento. A visão administrativa de informações operacionais SHALL obedecer a admin-observability, sem conceder leitura do conteúdo privado.

#### Scenario: Acesso a arquivo alheio
- **WHEN** um usuário tenta baixar ou consultar o upload de outro usuário
- **THEN** o acesso é negado sem entregar bytes ou metadados privados

#### Scenario: Retomada após novo login
- **WHEN** o proprietário autentica novamente após fechar o navegador ou expirar a sessão
- **THEN** pode recuperar e retomar suas operações persistidas sem acesso às transferências de outros usuários

#### Scenario: Parte de operação alheia
- **WHEN** outro usuário conhece o identificador de uma operação e tenta enviar uma parte ou cancelá-la
- **THEN** a requisição é negada e a operação original permanece inalterada

### Requirement: Encerramento de sessão
O sistema SHALL invalidar a sessão encerrada para todos os nós elegíveis.

#### Scenario: Logout seguido de troca de nó
- **WHEN** uma sessão é encerrada no nó 1 e reutilizada no nó 3
- **THEN** a requisição é rejeitada como não autenticada
