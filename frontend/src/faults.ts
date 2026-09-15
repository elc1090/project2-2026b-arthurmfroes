import { api, APIError } from "./api";
export const faultModes = {
  backend: "Backend",
  sql: "Banco local",
  storage: "Object storage",
  control: "Comunicação de controle",
  total: "Nó inteiro",
} as const;
export type FaultMode = keyof typeof faultModes | "none";
export function faultLabel(mode: FaultMode | undefined): string {
  return mode === "none"
    ? "Nenhuma falha simulada"
    : mode && mode in faultModes
      ? `Falha simulada: ${faultModes[mode as keyof typeof faultModes]}`
      : "Simulação não informada";
}
export async function setFault(
  id: string,
  mode: FaultMode,
  signal: AbortSignal,
): Promise<void> {
  await api(
    `/admin/nodes/${encodeURIComponent(id)}/fault`,
    "POST",
    { mode },
    signal,
  );
}
export function faultError(error: unknown): string {
  if (error instanceof APIError && error.status === 403)
    return "Esta conta não tem permissão para simular falhas.";
  if (error instanceof APIError && error.status === 404)
    return "Simulação indisponível neste ambiente ou nó não encontrado.";
  return "Não foi possível confirmar a solicitação. Consulte a observação do nó ou repita a mesma ação; ela é idempotente.";
}
