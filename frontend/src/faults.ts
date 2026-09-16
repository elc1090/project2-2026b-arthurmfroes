import { api, APIError } from "./api";
import type {
  FaultAction,
  FaultActionKind,
  FaultComponent,
} from "./admin-model";

export async function requestFaultAction(
  nodeID: string,
  component: FaultComponent,
  action: FaultActionKind,
  signal: AbortSignal,
): Promise<FaultAction> {
  return api(
    "/admin/fault-actions",
    "POST",
    { node_id: nodeID, component, action },
    signal,
  );
}

export function faultConfirmation(
  nodeLabel: string,
  componentLabel: string,
  component: FaultComponent,
  action: FaultActionKind,
): string {
  if (action === "restore")
    return `Restaurar ${componentLabel} em ${nodeLabel}? A readmissão continuará dependendo da saúde e da sincronização do nó.`;
  const quorum =
    component === "sql" || component === "node"
      ? " Esta ação pode remover o quorum do banco e interromper decisões do cluster."
      : "";
  return `Interromper ${componentLabel} em ${nodeLabel}? O cluster não será avisado e precisará detectar a queda pelas sondagens.${quorum}`;
}

export async function confirmFaultAction(
  confirm: (message: string) => boolean,
  nodeID: string,
  nodeLabel: string,
  component: FaultComponent,
  componentLabel: string,
  action: FaultActionKind,
  signal: AbortSignal,
): Promise<FaultAction | null> {
  if (!confirm(faultConfirmation(nodeLabel, componentLabel, component, action)))
    return null;
  return requestFaultAction(nodeID, component, action, signal);
}
export function faultError(error: unknown): string {
  if (error instanceof APIError && error.status === 403)
    return "Esta conta não tem permissão para controlar a infraestrutura.";
  if (error instanceof APIError && error.status === 404)
    return "O nó ou componente não está cadastrado no atuador.";
  if (error instanceof APIError && error.status === 409)
    return "A ação conflita com o estado atual do serviço. Atualize o painel e tente novamente.";
  if (error instanceof APIError && error.status === 503)
    return "O atuador de infraestrutura está indisponível.";
  return "Não foi possível confirmar a solicitação. Consulte o estado da ação antes de repetir.";
}
