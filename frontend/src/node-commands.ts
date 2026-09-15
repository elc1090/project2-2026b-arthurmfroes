import { api, APIError } from "./api";
export type NodeOperation = {
  id: string;
  idempotency_key?: string;
  node_id: string;
  kind: "join" | "retire" | "replace";
  stage:
    "preflight" | "sql" | "storage" | "complete" | "cancelled" | "remove_old";
  manager_term: number;
  last_error: string | null;
};
export type Registration = {
  node_id: string;
  backend_endpoint: string;
  database_endpoint: string;
  storage_endpoint: string;
  credential_profile: "default";
};
export type NodeCommand = {
  idempotency_key: string;
  kind: "join" | "retire";
  node_id: string;
  registration?: Registration;
};
export class PendingCommands {
  private key: string;
  constructor(
    owner: string,
    private storage?: Pick<Storage, "getItem" | "setItem">,
  ) {
    this.key = `acervo:node-commands:${owner}`;
  }
  private backend(): Pick<Storage, "getItem" | "setItem"> {
    return this.storage ?? globalThis.localStorage;
  }
  list(): NodeCommand[] {
    try {
      const value = this.backend().getItem(this.key);
      const parsed: unknown = value ? JSON.parse(value) : [];
      if (!Array.isArray(parsed)) return [];
      return parsed.filter((entry): entry is NodeCommand => {
        if (
          !entry ||
          typeof entry !== "object" ||
          typeof entry.idempotency_key !== "string" ||
          typeof entry.node_id !== "string"
        )
          return false;
        if (entry.kind === "retire") return true;
        const r = entry.registration;
        return (
          entry.kind === "join" &&
          r &&
          typeof r.node_id === "string" &&
          typeof r.backend_endpoint === "string" &&
          typeof r.database_endpoint === "string" &&
          typeof r.storage_endpoint === "string" &&
          r.credential_profile === "default"
        );
      });
    } catch {
      return [];
    }
  }
  save(command: NodeCommand) {
    try {
      this.backend().setItem(
        this.key,
        JSON.stringify([
          ...this.list().filter(
            (c) => c.idempotency_key !== command.idempotency_key,
          ),
          command,
        ]),
      );
    } catch {
      throw new CommandPersistenceError();
    }
  }
  remove(key: string) {
    // A confirmed command is already discoverable through the backend. If local
    // cleanup fails, retaining its idempotency key is safe for the next retry.
    try {
      this.backend().setItem(
        this.key,
        JSON.stringify(this.list().filter((c) => c.idempotency_key !== key)),
      );
    } catch {
      /* Retry cleanup on the next successful observation. */
    }
  }
}
export async function executeNodeCommand(
  command: NodeCommand,
  pending: PendingCommands,
  signal: AbortSignal,
): Promise<NodeOperation> {
  if (command.registration) {
    for (const endpoint of [
      command.registration.backend_endpoint,
      command.registration.database_endpoint,
      command.registration.storage_endpoint,
    ]) {
      let url: URL;
      try {
        url = new URL(endpoint);
      } catch {
        throw new APIError(400, "invalid_endpoint");
      }
      if (url.username || url.password || url.search)
        throw new APIError(400, "endpoint_password_forbidden");
    }
  }
  pending.save(command);
  const path =
    command.kind === "join"
      ? "/admin/nodes"
      : `/admin/nodes/${encodeURIComponent(command.node_id)}/retire`;
  const body =
    command.kind === "join"
      ? { ...command.registration, idempotency_key: command.idempotency_key }
      : { idempotency_key: command.idempotency_key };
  const op = await api<NodeOperation>(path, "POST", body, signal);
  pending.remove(command.idempotency_key);
  return op;
}
export const operationStages = {
  preflight: "Verificando condições",
  sql: "Configurando banco",
  storage: "Configurando armazenamento",
  remove_old: "Removendo associação antiga",
  complete: "Etapas concluídas",
  cancelled: "Solicitação cancelada",
};
export function nodeOperationError(code: string | null): string {
  if (!code) return "";
  if (code === "topology_blocked" || code === "cockroach_topology_blocked")
    return "A topologia impede esta operação. Confira o quorum e os requisitos dos três bancos antes de tentar novamente.";
  return "A operação encontrou um impedimento. Consulte o estado dos serviços; o gerenciador mantém o acompanhamento.";
}
export class CommandPersistenceError extends Error {}
export function nodeCommandError(error: unknown): string {
  if (error instanceof CommandPersistenceError)
    return "Não foi possível preservar a chave no navegador. A solicitação não foi enviada. Libere o armazenamento local antes de tentar novamente.";
  if (error instanceof APIError && error.code === "cockroach_topology_blocked")
    return "A retirada exige destinos suficientes para três réplicas SQL. Confira a configuração da topologia; a saúde dos nós, sozinha, não atende essa condição.";
  if (error instanceof APIError && error.status === 409)
    return "A solicitação conflita com a identidade do nó ou com outra operação. Confira os dados.";
  if (error instanceof APIError && error.status === 400)
    return "Confira a identidade e os endpoints do nó provisionado.";
  if (error instanceof APIError && error.status === 403)
    return "Esta conta não tem permissão para administrar os nós.";
  return "A resposta não foi confirmada. A solicitação foi preservada para repetir com a mesma chave.";
}

export function canCancelNodeOperation(op: NodeOperation): boolean {
  return op.kind === "retire" && op.stage === "preflight";
}
export async function cancelNodeOperation(
  op: NodeOperation,
  signal: AbortSignal,
): Promise<void> {
  if (!canCancelNodeOperation(op))
    throw new APIError(409, "step_not_cancellable");
  await api(
    `/admin/node-operations/${encodeURIComponent(op.id)}/cancel`,
    "POST",
    {},
    signal,
  );
}

export const nodeOperationKinds = {
  join: "Associação",
  retire: "Retirada",
  replace: "Recuperação de armazenamento",
};
