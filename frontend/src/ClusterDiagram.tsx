import {
  componentNames,
  componentState,
  incidentSteps,
  nodeStates,
  observationAge,
  observationLabel,
  reasonLabel,
  type AdminNode,
  type AdminView,
} from "./admin-model";

const date = (value: string | null) =>
  value ? new Date(value).toLocaleString("pt-BR") : "Não informado";

function healthClass(node: AdminNode, component: string): string {
  const state = componentState(node.health, component);
  return state === "Saudável"
    ? "healthy"
    : state === "Falha"
      ? "failed"
      : "unknown";
}

export function ClusterDiagram({
  view,
  elapsed,
  selectedID,
  onSelect,
}: {
  view: AdminView;
  elapsed: number;
  selectedID: string | null;
  onSelect: (id: string) => void;
}) {
  const routed = view.nodes.filter((node) => node.routed).length;
  return (
    <section className="cluster-diagram" aria-labelledby="architecture-title">
      <div className="diagram-heading">
        <div>
          <p className="eyebrow">Arquitetura ao vivo</p>
          <h2 id="architecture-title">Como as requisições chegam aos nós</h2>
        </div>
        <span className="route-count" aria-live="polite">
          {routed} {routed === 1 ? "nó na rota" : "nós na rota"}
        </span>
      </div>
      <div className="load-balancer" aria-label={`Load balancer, ${routed} nós ativos`}>
        <span>Entrada</span>
        <strong>Load balancer</strong>
        <small>Encaminha somente para rotas publicadas</small>
      </div>
      <div className="route-trunk" aria-hidden="true" />
      <div className="diagram-nodes">
        {view.nodes.map((node) => {
          const age = observationAge(node.observed_at, view.observed_at, elapsed);
          const stale = age === null || age > 15000;
          const selected = selectedID === node.id;
          return (
            <div className="node-route" key={node.id}>
              <div
                className={`route-line ${node.routed ? "connected" : "disconnected"} ${node.state === "syncing" ? "syncing" : ""}`}
                role="img"
                aria-label={
                  node.routed
                    ? `${node.node_id} está na rota`
                    : `${node.node_id} está fora da rota`
                }
              >
                <span>{node.routed ? "Na rota" : "Fora da rota"}</span>
              </div>
              <button
                type="button"
                className={`diagram-node state-${node.state} ${stale ? "stale" : ""} ${selected ? "selected" : ""}`}
                aria-pressed={selected}
                aria-label={`${node.node_id}, ${nodeStates[node.state] || "estado desconhecido"}, ${observationLabel(age)}`}
                onClick={() => onSelect(node.id)}
              >
                <span className="node-title">
                  <strong>{node.node_id}</strong>
                  {node.id === view.manager_id && (
                    <span className="manager-badge">Gerenciador</span>
                  )}
                </span>
                <span className="node-status">
                  {stale
                    ? "Informação desatualizada"
                    : nodeStates[node.state] || "Estado desconhecido"}
                </span>
                <span className="component-stack">
                  {Object.entries(componentNames).map(([key, label]) => (
                    <span className="component-row" key={key}>
                      <span>{label}</span>
                      <span className={`health ${healthClass(node, key)}`}>
                        {stale ? "Sem confirmação" : componentState(node.health, key)}
                      </span>
                    </span>
                  ))}
                </span>
                <span className="node-reason">{reasonLabel(node.reason)}</span>
              </button>
            </div>
          );
        })}
      </div>
      <p className="storage-relation">
        <span aria-hidden="true">↔</span> Bancos e object storages mantêm cópias
        distribuídas entre os nós disponíveis.
      </p>
    </section>
  );
}

export function NodeIncident({
  view,
  node,
}: {
  view: AdminView;
  node: AdminNode;
}) {
  const steps = incidentSteps(
    node,
    view.fault_actions,
    view.publication_generation,
    view.events,
  );
  return (
    <section className="node-inspector" aria-labelledby="selected-node-title">
      <div className="inspector-heading">
        <div>
          <p className="eyebrow">Nó selecionado</p>
          <h2 id="selected-node-title">{node.node_id}</h2>
        </div>
        <span className={`node-state state-${node.state}`}>
          {nodeStates[node.state] || "Estado desconhecido"}
        </span>
      </div>
      <p>{reasonLabel(node.reason)}</p>
      <h3>O que está acontecendo</h3>
      {steps.length === 0 ? (
        <p className="admin-empty">
          Nenhuma ação de infraestrutura registrada para este nó.
        </p>
      ) : (
        <ol className="incident-steps">
          {steps.map((step) => (
            <li className={step.complete ? "complete" : "pending"} key={step.id}>
              <span className="step-mark" aria-hidden="true">
                {step.complete ? "✓" : "·"}
              </span>
              <span>
                <strong>{step.label}</strong>
                <small>{step.detail}</small>
              </span>
            </li>
          ))}
        </ol>
      )}
      <details className="technical-details">
        <summary>Detalhes técnicos</summary>
        <dl>
          <div><dt>Configuração</dt><dd>v{view.version}</dd></div>
          <div><dt>Geração publicada</dt><dd>{view.publication_generation}</dd></div>
          <div><dt>Geração sincronizada</dt><dd>{node.synced_generation}</dd></div>
          <div><dt>Mandato</dt><dd>{view.manager_term}</dd></div>
          <div><dt>Última transição</dt><dd>{date(node.transitioned_at)}</dd></div>
          <div><dt>Concessão até</dt><dd>{date(view.lease_expires_at)}</dd></div>
        </dl>
      </details>
    </section>
  );
}
