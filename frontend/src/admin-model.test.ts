import { test } from "node:test";
import assert from "node:assert/strict";
import {
  componentState,
  incidentSteps,
  leaseCurrent,
  leaseRemainingSeconds,
  nodeLifecycleSteps,
  observationAge,
  observationLabel,
  reasonLabel,
  safeEventDetail,
  type AdminNode,
  type FaultAction,
  type AdminView,
} from "./admin-model";

test("observação ausente ou velha não se transforma em saúde atual", () => {
  const snapshot = "2026-09-15T12:00:00Z";
  assert.equal(observationAge(null, snapshot, 1000), null);
  assert.equal(observationLabel(null), "Sem observação disponível");
  assert.equal(observationAge("2026-09-15T11:59:50Z", snapshot, 6000), 16000);
  assert.match(observationLabel(16000), /desatualizada/);
  assert.equal(componentState(null, "sql"), "Sem observação");
  assert.equal(componentState({ sql: false, storage: true }, "sql"), "Falha");
  assert.equal(componentState({ storage: true }, "storage"), "Saudável");
  assert.equal(componentState({ sql: "true" }, "sql"), "Sem observação");
});

const node: AdminNode = {
  id: "id-2",
  node_id: "node-2",
  state: "ready",
  health: { backend: true, sql: true, storage: true, control: true },
  reason: null,
  observed_at: "2026-09-16T12:00:00Z",
  transitioned_at: "2026-09-16T12:00:00Z",
  synced_generation: 7,
  routed: true,
};

function action(overrides: Partial<FaultAction> = {}): FaultAction {
  return {
    id: "action-1",
    node_id: "node-2",
    component: "storage",
    action: "stop",
    status: "stopped",
    error: null,
    updated_at: "2026-09-16T12:00:01Z",
    ...overrides,
  };
}

test("sequência não antecipa observação, exclusão nem mudança de rota", () => {
  const steps = incidentSteps(node, [action()], 7);
  assert.equal(steps.find((step) => step.id === "provider-stop")?.complete, true);
  assert.equal(
    steps.find((step) => step.id === "failure-observed")?.complete,
    false,
  );
  assert.equal(steps.find((step) => step.id === "node-excluded")?.complete, false);
  assert.equal(steps.find((step) => step.id === "route-updated")?.complete, false);
});

test("sequência distingue exclusão, restauração, sincronização e readmissão", () => {
  const failed = {
    ...node,
    state: "unavailable",
    health: { storage: false },
    routed: false,
    synced_generation: 5,
  };
  const beforeRestore = incidentSteps(failed, [action()], 7);
  assert.equal(beforeRestore.find((step) => step.id === "node-excluded")?.complete, true);
  assert.equal(beforeRestore.find((step) => step.id === "provider-restore")?.complete, false);

  const recovered = incidentSteps(
    node,
    [
      action(),
      action({
        id: "action-2",
        action: "restore",
        status: "restored",
        updated_at: "2026-09-16T12:01:00Z",
      }),
    ],
    7,
    [
      {
        id: "event-excluded",
        node_id: "id-2",
        kind: "node_excluded",
        configuration_version: 3,
        manager_term: 1,
        details: { reason: "storage probe failed" },
        at: "2026-09-16T12:00:10Z",
      },
      {
        id: "event-admitted",
        node_id: "id-2",
        kind: "node_admitted",
        configuration_version: 4,
        manager_term: 1,
        details: null,
        at: "2026-09-16T12:01:30Z",
      },
    ],
  );
  assert.equal(recovered.find((step) => step.id === "node-excluded")?.complete, true);
  assert.equal(recovered.find((step) => step.id === "failure-observed")?.complete, true);
  assert.equal(recovered.find((step) => step.id === "route-updated")?.complete, true);
  assert.equal(recovered.find((step) => step.id === "synchronization")?.complete, true);
  assert.equal(recovered.find((step) => step.id === "readmitted")?.complete, true);
  assert.equal(recovered.find((step) => step.id === "route-restored")?.complete, true);
});

test("histórico só apresenta detalhes conhecidos e sanitizados", () => {
  const base = {
    id: "event-1",
    node_id: "id-2",
    kind: "node_excluded",
    configuration_version: 4,
    manager_term: 2,
    at: "2026-09-16T12:00:00Z",
  };
  assert.equal(
    safeEventDetail({ ...base, details: { component: "storage" } }),
    "Object storage",
  );
  assert.equal(
    safeEventDetail({ ...base, details: { status: "password=secret" } }),
    null,
  );
});
test("concessão envelhece com tempo decorrido sem depender do relógio civil local", () => {
  const view = {
    manager_id: "manager",
    observed_at: "2026-09-15T12:00:00Z",
    lease_expires_at: "2026-09-15T12:00:05Z",
  } as AdminView;
  assert.equal(leaseCurrent(view, 4999), true);
  assert.equal(leaseCurrent(view, 5000), false);
  assert.equal(leaseRemainingSeconds(view, 1), 5);
  assert.equal(leaseRemainingSeconds(view, 4999), 1);
  assert.equal(leaseRemainingSeconds(view, 5000), 0);
  assert.equal(leaseCurrent({ ...view, manager_id: null }, 0), false);
});
test("ciclo do nó expõe sincronização e readmissão separadamente", () => {
  const syncing = nodeLifecycleSteps(
    { ...node, state: "syncing", routed: false, synced_generation: 5 },
    7,
    [],
  );
  assert.equal(syncing.find((step) => step.id === "node-synchronization")?.complete, false);
  assert.match(
    syncing.find((step) => step.id === "node-synchronization")?.detail || "",
    /geração 5 de 7/,
  );
  assert.equal(syncing.find((step) => step.id === "node-readmitted")?.complete, false);

  const ready = nodeLifecycleSteps(node, 7, [
    {
      id: "admitted",
      node_id: node.id,
      kind: "node_admitted",
      configuration_version: 4,
      manager_term: 3,
      details: null,
      at: "2026-09-16T12:01:00Z",
    },
  ]);
  assert.equal(ready.find((step) => step.id === "node-synchronization")?.complete, true);
  assert.match(ready.find((step) => step.id === "node-readmitted")?.detail || "", /mandato 3/);
});
test("metadados desconhecidos de saúde e erro não são despejados na tela", () => {
  assert.equal(
    componentState({ storage: "password=secret" }, "storage"),
    "Sem observação",
  );
  assert.equal(reasonLabel("SQL password=secret").includes("secret"), false);
});
