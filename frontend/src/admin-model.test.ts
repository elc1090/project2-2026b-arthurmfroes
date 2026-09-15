import { test } from "node:test";
import assert from "node:assert/strict";
import {
  componentState,
  leaseCurrent,
  observationAge,
  observationLabel,
  reasonLabel,
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
test("concessão envelhece com tempo decorrido sem depender do relógio civil local", () => {
  const view = {
    manager_id: "manager",
    observed_at: "2026-09-15T12:00:00Z",
    lease_expires_at: "2026-09-15T12:00:05Z",
  } as AdminView;
  assert.equal(leaseCurrent(view, 4999), true);
  assert.equal(leaseCurrent(view, 5000), false);
  assert.equal(leaseCurrent({ ...view, manager_id: null }, 0), false);
});
test("metadados desconhecidos de saúde e erro não são despejados na tela", () => {
  assert.equal(
    componentState({ storage: "password=secret" }, "storage"),
    "Sem observação",
  );
  assert.equal(reasonLabel("SQL password=secret").includes("secret"), false);
});
