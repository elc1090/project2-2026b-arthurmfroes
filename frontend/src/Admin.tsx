import { useEffect, useRef, useState } from "react";
import { api, APIError, message } from "./api";
import {
  eventLabel,
  operationStates,
  safeEventDetail,
  type AdminView,
} from "./admin-model";
import { NodeManagement } from "./NodeManagement";
import { FaultControls } from "./FaultControls";
import { ClusterDiagram, NodeIncident } from "./ClusterDiagram";
import { operationError } from "./queue";
function date(value: string | null) {
  return value ? new Date(value).toLocaleString("pt-BR") : "Não informado";
}
export function Admin({
  expired,
  owner,
}: {
  expired: () => void;
  owner: string;
}) {
  const [view, setView] = useState<AdminView | null>(null),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false),
    [selectedID, setSelectedID] = useState<string | null>(null),
    [clock, setClock] = useState(performance.now());
  const receivedAt = useRef(performance.now());
  const pollNow = useRef<() => void>(() => {});
  const expire = useRef(expired);
  expire.current = expired;
  useEffect(() => {
    let disposed = false,
      inFlight = false,
      timer: ReturnType<typeof setTimeout>;
    let current: AbortController | undefined;
    async function poll() {
      if (disposed || inFlight) return;
      clearTimeout(timer);
      inFlight = true;
      setBusy(true);
      current = new AbortController();
      const timeout = setTimeout(() => current?.abort(), 8000);
      try {
        const result = await api<AdminView>(
          "/admin/cluster",
          "GET",
          undefined,
          current.signal,
        );
        if (disposed) return;
        receivedAt.current = performance.now();
        setClock(receivedAt.current);
        setView(result);
        setSelectedID((selected) =>
          selected && result.nodes.some((node) => node.id === selected)
            ? selected
            : result.nodes[0]?.id || null,
        );
        setError("");
      } catch (e) {
        if (disposed) return;
        if (e instanceof APIError && e.status === 401) expire.current();
        else
          setError(
            e instanceof APIError && e.status === 403
              ? "Esta conta não tem permissão para consultar o painel."
              : "Não foi possível atualizar o painel. " + message(e),
          );
      } finally {
        clearTimeout(timeout);
        inFlight = false;
        if (!disposed) {
          setBusy(false);
          timer = setTimeout(poll, 5000);
        }
      }
    }
    pollNow.current = () => void poll();
    void poll();
    const ageTimer = setInterval(() => setClock(performance.now()), 1000);
    return () => {
      disposed = true;
      clearTimeout(timer);
      clearInterval(ageTimer);
      current?.abort();
      pollNow.current = () => {};
    };
  }, []);
  const elapsed = Math.max(0, clock - receivedAt.current);
  const selected = view?.nodes.find((node) => node.id === selectedID);
  return (
    <section className="admin">
      <div className="title-row">
        <div>
          <h1>Administração</h1>
          <p>Estado dos nós e das confirmações de armazenamento.</p>
        </div>
        <button onClick={() => pollNow.current()} disabled={busy}>
          {busy ? "Atualizando…" : "Atualizar"}
        </button>
      </div>
      <p className="admin-note">
        Acompanhamento automático a cada 5 segundos. O gerenciador continua
        operando quando este painel está fechado.
      </p>
      {error && (
        <p className="error" role="alert">
          {error}
          {view && " Os dados abaixo são da última consulta bem-sucedida."}
        </p>
      )}
      {!view ? (
        <p className="empty">
          {busy ? "Consultando o cluster…" : "Sem dados disponíveis."}
        </p>
      ) : (
        <>
          <ClusterDiagram
            view={view}
            elapsed={elapsed}
            selectedID={selectedID}
            onSelect={setSelectedID}
          />
          {selected && (
            <div className="admin-detail-grid">
              <NodeIncident view={view} node={selected} />
              {selected.state !== "removed" && (
                <FaultControls
                  node={selected}
                  actions={view.fault_actions}
                  available={view.fault_control_available}
                  unavailableReason={view.fault_control_error}
                  refresh={() => pollNow.current()}
                  expired={() => expire.current()}
                />
              )}
            </div>
          )}
          <NodeManagement
            owner={owner}
            nodes={view.nodes}
            expired={() => expire.current()}
            refresh={() => pollNow.current()}
          />
          <h2 className="admin-section-title">
            Operações pendentes <span>{view.operations.length}</span>
          </h2>
          <p className="admin-note">
            Somente metadados operacionais. Arquivos privados não são acessados
            pelo painel. Partes recebidas são registros de recebimento, não uma
            garantia de disponibilidade atual.
          </p>
          {view.operations.length === 0 ? (
            <p className="admin-empty">
              Nenhuma operação pendente nesta consulta.
            </p>
          ) : (
            <div className="admin-table-wrap">
              <table className="admin-table">
                <thead>
                  <tr>
                    <th>Operação</th>
                    <th>Estado</th>
                    <th>Partes recebidas</th>
                    <th>Cópias confirmadas</th>
                    <th>Erro operacional</th>
                  </tr>
                </thead>
                <tbody>
                  {view.operations.map((op) => (
                    <tr key={op.id}>
                      <td>
                        <code>{op.id}</code>
                      </td>
                      <td>
                        {operationStates[op.phase] ||
                          operationStates[op.status] ||
                          "Estado desconhecido"}
                      </td>
                      <td>
                        {op.received_parts} / {op.parts}
                      </td>
                      <td>
                        {op.copies} / {op.required_copies}
                        <ul className="site-receipts">
                          {op.sites.map((site) => (
                            <li key={site.id}>
                              {site.node_id}:{" "}
                              {site.confirmed ? "Confirmada" : "Pendente"}
                            </li>
                          ))}
                        </ul>
                      </td>
                      <td>
                        {op.error_code
                          ? operationError(op.error_code).message
                          : "Nenhum erro registrado"}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <details className="event-history">
            <summary>Eventos técnicos recentes ({view.events.length})</summary>
            {view.events.length === 0 ? (
              <p className="admin-empty">Nenhum evento registrado.</p>
            ) : (
              <ol className="event-list">
                {view.events.map((event) => {
                  const detail = safeEventDetail(event);
                  return (
                    <li key={event.id}>
                      <time>{date(event.at)}</time>
                      <span>{eventLabel(event.kind)}</span>
                      <small>
                        {view.nodes.find((node) => node.id === event.node_id)
                          ?.node_id || "Cluster"}{" "}
                        · configuração v{event.configuration_version} · mandato{" "}
                        {event.manager_term}
                        {detail ? ` · ${detail}` : ""}
                      </small>
                    </li>
                  );
                })}
              </ol>
            )}
          </details>
        </>
      )}
    </section>
  );
}
