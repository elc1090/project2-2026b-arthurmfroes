import type { FaultMode } from "./faults";
export type AdminNode = {
  id: string;
  node_id: string;
  state: string;
  health: unknown;
  reason: string | null;
  observed_at: string | null;
  transitioned_at: string;
  synced_generation: number;
  simulation: FaultMode;
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
  at: string;
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
  simulation_enabled: boolean;
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
    fault_changed: "Simulação de falha alterada",
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
