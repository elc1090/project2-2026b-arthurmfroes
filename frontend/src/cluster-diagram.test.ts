import { test } from "node:test";
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ClusterDiagram, LeaderElection, NodeIncident } from "./ClusterDiagram";
import { FaultControls } from "./FaultControls";
import type { AdminNode, AdminView } from "./admin-model";

const node: AdminNode = {
  id: "id-1",
  node_id: "node-1",
  state: "ready",
  health: { backend: true, sql: true, storage: true, control: true },
  reason: "recovery completed",
  observed_at: "2026-09-16T12:00:00Z",
  transitioned_at: "2026-09-16T11:59:00Z",
  synced_generation: 8,
  routed: true,
};
const view: AdminView = {
  nodes: [node],
  operations: [],
  events: [],
  fault_actions: [],
  version: 4,
  publication_generation: 8,
  manager_id: "id-1",
  manager_term: 3,
  lease_expires_at: "2026-09-16T12:00:15Z",
  observed_at: "2026-09-16T12:00:00Z",
  fault_control_available: true,
  fault_control_error: null,
};

test("diagrama identifica rota, gerenciador e componentes com botão selecionável", () => {
  const html = renderToStaticMarkup(
    createElement(ClusterDiagram, {
      view,
      elapsed: 0,
      selectedID: "id-1",
      onSelect: () => {},
    }),
  );
  assert.match(html, /Load balancer/);
  assert.match(html, /1 nó na rota/);
  assert.match(html, /Gerenciador/);
  assert.match(html, /aria-pressed="true"/);
  assert.match(html, /Backend/);
  assert.match(html, /Object storage/);
  assert.match(html, /Mandato 3: node-1 é o gerenciador/);
  assert.match(html, /Concessão válida por mais 15 s/);
});

test("eleição expirada mostra disputa e histórico de mandatos", () => {
  const html = renderToStaticMarkup(
    createElement(LeaderElection, {
      view: {
        ...view,
        events: [
          {
            id: "election-3",
            node_id: "id-1",
            kind: "manager_elected",
            configuration_version: 4,
            manager_term: 3,
            details: null,
            at: "2026-09-16T12:00:00Z",
          },
        ],
      },
      elapsed: 15000,
    }),
  );
  assert.match(html, /eleição em andamento/);
  assert.match(html, /O último titular foi node-1/);
  assert.match(html, /Mandato 3/);
});

test("nó sincronizando mostra geração no próprio diagrama", () => {
  const html = renderToStaticMarkup(
    createElement(ClusterDiagram, {
      view: {
        ...view,
        nodes: [{ ...node, state: "syncing", routed: false, synced_generation: 5 }],
      },
      elapsed: 0,
      selectedID: null,
      onSelect: () => {},
    }),
  );
  assert.match(html, /Sincronizando publicações/);
  assert.match(html, /geração 5 de 8/);
});

test("observação velha nunca aparece como nó saudável atual", () => {
  const html = renderToStaticMarkup(
    createElement(ClusterDiagram, {
      view,
      elapsed: 16000,
      selectedID: null,
      onSelect: () => {},
    }),
  );
  assert.match(html, /Informação desatualizada/);
  assert.match(html, /Sem confirmação/);
});

test("diagrama distingue nós indisponíveis, sincronizando e removidos", () => {
  const states: AdminNode[] = [
    { ...node, id: "unavailable", node_id: "node-2", state: "unavailable", routed: false },
    { ...node, id: "syncing", node_id: "node-3", state: "syncing", routed: false },
    { ...node, id: "removed", node_id: "node-4", state: "removed", routed: false },
  ];
  const html = renderToStaticMarkup(
    createElement(ClusterDiagram, {
      view: { ...view, nodes: states, manager_id: null },
      elapsed: 0,
      selectedID: null,
      onSelect: () => {},
    }),
  );
  assert.match(html, /Indisponível/);
  assert.match(html, /Sincronizando/);
  assert.match(html, /Retirado/);
  assert.match(html, /route-line disconnected/);
  assert.match(html, /route-line disconnected syncing/);
});

test("detalhes técnicos e controles indisponíveis preservam a observação", () => {
  const incident = renderToStaticMarkup(
    createElement(NodeIncident, { view, node }),
  );
  const controls = renderToStaticMarkup(
    createElement(FaultControls, {
      node,
      actions: [],
      available: false,
      unavailableReason: "Configuração do atuador incompleta.",
      refresh: () => {},
      expired: () => {},
    }),
  );
  assert.match(incident, /<details class="technical-details">/);
  assert.match(incident, /Nenhuma ação de infraestrutura/);
  assert.match(controls, /Configuração do atuador incompleta/);
  assert.match(controls, /disabled=""/);
  assert.doesNotMatch(controls, /Simulação de desenvolvimento/);
});

test("controle apresenta resultado parcial sem ocultar o erro sanitizado", () => {
  const controls = renderToStaticMarkup(
    createElement(FaultControls, {
      node,
      actions: [
        {
          id: "action-2",
          node_id: "node-1",
          component: "node",
          action: "stop",
          status: "failed",
          error: "Falha parcial confirmada",
          updated_at: "2026-09-16T12:01:00Z",
          results: [
            { component: "backend", status: "stopped", error: null },
            { component: "sql", status: "failed", error: "Destino recusou" },
          ],
        },
      ],
      available: true,
      unavailableReason: null,
      refresh: () => {},
      expired: () => {},
    }),
  );
  assert.match(controls, /Falha parcial confirmada/);
  assert.match(controls, /Backend:.*Parada confirmada/);
  assert.match(controls, /Banco local:.*Falhou.*Destino recusou/);
});
