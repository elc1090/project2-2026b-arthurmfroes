export type AdminNode = {
  id: string;
  node_id: string;
  state: string;
  health: unknown;
  reason: string | null;
  observed_at: string | null;
  transitioned_at: string;
  synced_generation: number;
  routed: boolean;
};
export type AdminOperation = {
  sites: { id: string; node_id: string; confirmed: boolean }[];
  id: string;
  status: string;
  phase: string;
  parts: number;
  received_parts: number;
  copies: number;
  required_copies: number;
  error_code: string | null;
};
export type AdminEvent = {
  id: string;
  node_id: string | null;
  kind: string;
  configuration_version: number;
  manager_term: number;
  details: EventDetails | null;
  at: string;
};
export type FaultComponent = "backend" | "sql" | "storage" | "node";
export type FaultActionKind = "stop" | "restore";
export type FaultActionStatus =
  | "requested"
  | "running"
  | "stopped"
  | "restored"
  | "partial"
  | "failed"
  | "unknown";
export type EventDetails = {
  reason?: string;
  action?: string;
  component?: FaultComponent;
  status?: string;
};
export type FaultAction = {
  id: string;
  node_id: string;
  component: FaultComponent;
  action: FaultActionKind;
  status: FaultActionStatus;
  error: string | null;
  updated_at: string;
  results?: {
    component: Exclude<FaultComponent, "node">;
    status: "stopped" | "restored" | "failed" | "unknown";
    error: string | null;
  }[];
};
export type AdminView = {
  nodes: AdminNode[];
  operations: AdminOperation[];
  events: AdminEvent[];
  version: number;
  publication_generation: number;
  manager_id: string | null;
  manager_term: number;
  lease_expires_at: string | null;
  observed_at: string;
  fault_control_available: boolean;
  fault_control_error: string | null;
  fault_actions: FaultAction[];
};
export const nodeStates: Record<string, string> = {
  joining: "Associando",
  syncing: "Sincronizando",
  ready: "Pronto",
  unavailable: "Indisponível",
  removed: "Retirado",
};
export const operationStates: Record<string, string> = {
  pending: "Pendente",
  available: "Publicado",
  cancelled: "Cancelado",
  receiving: "Recebendo partes",
  confirming: "Confirmando armazenamento",
  awaiting_parts: "Aguardando partes",
  complete: "Concluído",
};
export const componentNames = {
  backend: "Backend",
  sql: "Banco local",
  storage: "Object storage",
  control: "Comunicação de controle",
};
export const faultComponentNames: Record<FaultComponent, string> = {
  backend: "Backend",
  sql: "Banco local",
  storage: "Object storage",
  node: "Nó inteiro",
};
export function componentState(health: unknown, component: string): string {
  if (!health || typeof health !== "object") return "Sem observação";
  const value = (health as Record<string, unknown>)[component];
  return value === true
    ? "Saudável"
    : value === false
      ? "Falha"
      : "Sem observação";
}
// The browser's clock need not match the database clock. Advance the snapshot's
// server time using elapsed client time, and never infer a membership decision.
export function observationAge(
  observed: string | null,
  snapshot: string,
  elapsed: number,
): number | null {
  if (!observed) return null;
  const age =
    Date.parse(snapshot) - Date.parse(observed) + Math.max(0, elapsed);
  return Number.isFinite(age) ? Math.max(0, age) : null;
}
export function observationLabel(age: number | null): string {
  if (age === null) return "Sem observação disponível";
  return age > 15000
    ? `Observação desatualizada · ${Math.floor(age / 1000)} s`
    : `Observado há ${Math.floor(age / 1000)} s`;
}
export function leaseCurrent(view: AdminView, elapsed: number): boolean {
  return (
    !!view.manager_id &&
    !!view.lease_expires_at &&
    Date.parse(view.lease_expires_at) >
      Date.parse(view.observed_at) + Math.max(0, elapsed)
  );
}
export function reasonLabel(reason: string | null): string {
  if (!reason) return "Nenhum motivo registrado";
  const reasons: Record<string, string> = {
    "backend/control probe unavailable":
      "Backend ou comunicação de controle inacessível",
    "control probe unavailable": "Comunicação de controle inacessível",
    "backend probe failed": "Falha na sondagem do backend",
    "control probe failed": "Falha na sondagem da comunicação de controle",
    "SQL probe failed": "Falha na sondagem do banco local",
    "storage probe failed": "Falha na sondagem do armazenamento",
    "local health failed": "Falha na verificação local",
    "node registered": "Nó registrado",
    "recovery completed": "Sincronização concluída",
  };
  return reasons[reason] || "O gerenciador registrou uma alteração operacional";
}
export function eventLabel(kind: string): string {
  const kinds: Record<string, string> = {
    node_registered: "Nó registrado",
    node_excluded: "Nó retirado do atendimento",
    node_admitted: "Nó admitido",
    node_syncing: "Sincronização iniciada",
    node_sync_started: "Sincronização iniciada",
    manager_acquired: "Gerenciador assumiu",
    manager_elected: "Gerenciador eleito",
    node_removed: "Retirada permanente",
  };
  return kinds[kind] || "Evento de controle";
}

export type IncidentStep = {
  id: string;
  label: string;
  complete: boolean;
  detail: string;
};

function actionCompleted(action: FaultAction | undefined): boolean {
  return !!action && ["stopped", "restored"].includes(action.status);
}

function componentFailed(node: AdminNode, component: FaultComponent): boolean {
  if (!node.health || typeof node.health !== "object") return false;
  const health = node.health as Record<string, unknown>;
  if (component === "node")
    return ["backend", "sql", "storage"].some((key) => health[key] === false);
  return health[component] === false;
}

export function incidentSteps(
  node: AdminNode,
  actions: FaultAction[],
  publicationGeneration: number,
  events: AdminEvent[] = [],
): IncidentStep[] {
  const ordered = actions
    .filter((item) => item.node_id === node.node_id)
    .sort((a, b) => Date.parse(a.updated_at) - Date.parse(b.updated_at));
  const stop = [...ordered].reverse().find((item) => item.action === "stop");
  const latestRestore = [...ordered]
    .reverse()
    .find((item) => item.action === "restore");
  const restore = stop
    ? [...ordered].reverse().find(
        (item) =>
          item.action === "restore" &&
          Date.parse(item.updated_at) >= Date.parse(stop.updated_at),
      )
    : latestRestore;
  if (!stop && !restore) return [];
  const affected = stop?.component || restore?.component || "node";
  const stopped = stop?.status === "stopped";
  const restored = actionCompleted(restore);
  const nodeEvents = events.filter(
    (event) =>
      (event.node_id === node.id || event.node_id === node.node_id) &&
      (!stop || Date.parse(event.at) >= Date.parse(stop.updated_at)),
  );
  const excluded =
    node.state === "unavailable" ||
    node.state === "removed" ||
    nodeEvents.some((event) => event.kind === "node_excluded");
  const readmitted =
    restored &&
    (nodeEvents.some((event) => event.kind === "node_admitted") ||
      node.state === "ready");
  return [
    {
      id: "provider-stop",
      label: "Infraestrutura interrompida",
      complete: stopped,
      detail: stop
        ? `Atuador: ${faultActionStatusLabel(stop.status)}`
        : "Nenhuma interrupção registrada",
    },
    {
      id: "failure-observed",
      label: "Falha observada pelo cluster",
      complete: componentFailed(node, affected),
      detail: componentFailed(node, affected)
        ? `${faultComponentNames[affected]} sem saúde`
        : "Aguardando sondagens",
    },
    {
      id: "node-excluded",
      label: "Nó retirado da composição elegível",
      complete: excluded,
      detail: excluded ? reasonLabel(node.reason) : "Aguardando o gerenciador",
    },
    {
      id: "route-updated",
      label: "Rota do balanceador atualizada",
      complete: !node.routed,
      detail: node.routed ? "O nó ainda recebe tráfego" : "Nó fora da rota",
    },
    {
      id: "provider-restore",
      label: "Infraestrutura restaurada",
      complete: restored,
      detail: restore
        ? `Atuador: ${faultActionStatusLabel(restore.status)}`
        : "Restauração ainda não solicitada",
    },
    {
      id: "synchronization",
      label: "Publicações sincronizadas",
      complete:
        restored &&
        node.state === "ready" &&
        node.synced_generation >= publicationGeneration,
      detail:
        node.state === "syncing"
          ? "Sincronização em andamento"
          : restored &&
              node.state === "ready" &&
              node.synced_generation >= publicationGeneration
            ? "Sincronização concluída"
            : "Aguardando restauração e sincronização",
    },
    {
      id: "readmitted",
      label: "Nó readmitido",
      complete: readmitted,
      detail:
        readmitted
          ? "Nó pronto"
          : "Aguardando decisão do gerenciador",
    },
    {
      id: "route-restored",
      label: "Nó voltou à rota",
      complete: restored && node.routed,
      detail: restored && node.routed ? "Recebendo tráfego" : "Fora da rota",
    },
  ];
}

export function faultActionStatusLabel(status: FaultActionStatus): string {
  const labels: Record<FaultActionStatus, string> = {
    requested: "Solicitada",
    running: "Em execução",
    stopped: "Parada confirmada",
    restored: "Restauração confirmada",
    partial: "Concluída parcialmente",
    failed: "Falhou",
    unknown: "Resultado desconhecido",
  };
  return labels[status];
}

export function faultResultStatusLabel(
  status: "stopped" | "restored" | "failed" | "unknown",
): string {
  return {
    stopped: "Parada confirmada",
    restored: "Restauração confirmada",
    failed: "Falhou",
    unknown: "Resultado desconhecido",
  }[status];
}

export function safeEventDetail(event: AdminEvent): string | null {
  if (!event.details) return null;
  if (event.details.reason) return reasonLabel(event.details.reason);
  if (event.details.component)
    return faultComponentNames[event.details.component];
  if (event.details.status) {
    const allowed: Record<string, string> = {
      requested: "Solicitada",
      running: "Em execução",
      stopped: "Parada confirmada",
      restored: "Restauração confirmada",
      partial: "Concluída parcialmente",
      failed: "Falhou",
      unknown: "Resultado desconhecido",
    };
    return allowed[event.details.status] || null;
  }
  return null;
}
