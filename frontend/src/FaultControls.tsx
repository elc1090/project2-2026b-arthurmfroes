import { useEffect, useRef, useState } from "react";
import { APIError } from "./api";
import {
  faultActionStatusLabel,
  faultComponentNames,
  faultResultStatusLabel,
  type AdminNode,
  type FaultAction,
  type FaultActionKind,
  type FaultComponent,
} from "./admin-model";
import { confirmFaultAction, faultError } from "./faults";

export function FaultControls({
  node,
  actions,
  available,
  unavailableReason,
  refresh,
  expired,
}: {
  node: AdminNode;
  actions: FaultAction[];
  available: boolean;
  unavailableReason: string | null;
  refresh: () => void;
  expired: () => void;
}) {
  const [component, setComponent] = useState<FaultComponent>("storage"),
    [busy, setBusy] = useState(false),
    [notice, setNotice] = useState(""),
    [error, setError] = useState("");
  const mounted = useRef(true),
    request = useRef<AbortController | null>(null);
  useEffect(
    () => () => {
      mounted.current = false;
      request.current?.abort();
    },
    [],
  );

  async function apply(action: FaultActionKind) {
    if (request.current || !available) return;
    const controller = new AbortController();
    request.current = controller;
    setBusy(true);
    setNotice("");
    setError("");
    const timer = setTimeout(() => controller.abort(), 10000);
    try {
      const result = await confirmFaultAction(
        window.confirm,
        node.node_id,
        node.node_id,
        component,
        faultComponentNames[component],
        action,
        controller.signal,
      );
      if (!result) return;
      if (!mounted.current) return;
      setNotice(
        action === "stop"
          ? "Ação enviada ao atuador. A retirada só aparecerá depois que o cluster observar a falha."
          : "Restauração enviada. O cluster ainda precisa observar a saúde, sincronizar e readmitir o nó.",
      );
      refresh();
    } catch (cause) {
      if (!mounted.current) return;
      if (cause instanceof APIError && cause.status === 401) expired();
      else setError(faultError(cause));
    } finally {
      clearTimeout(timer);
      request.current = null;
      if (mounted.current) setBusy(false);
    }
  }

  const recent = actions
    .filter((item) => item.node_id === node.node_id)
    .sort((a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at));
  return (
    <section className="fault-controls" aria-labelledby={`fault-${node.id}`}>
      <h3 id={`fault-${node.id}`}>Controlar infraestrutura</h3>
      <p>
        O atuador interrompe o serviço sem avisar o gerenciador. A retirada e a
        readmissão continuam automáticas.
      </p>
      {!available && (
        <p className="error" role="alert">
          {unavailableReason || "Controles de infraestrutura indisponíveis."}
        </p>
      )}
      <label>
        Componente em {node.node_id}
        <select
          value={component}
          onChange={(event) =>
            setComponent(event.target.value as FaultComponent)
          }
          disabled={busy || !available}
        >
          {Object.entries(faultComponentNames).map(([value, label]) => (
            <option value={value} key={value}>
              {label}
            </option>
          ))}
        </select>
      </label>
      {(component === "sql" || component === "node") && (
        <p className="fault-impact" role="note">
          Pode remover o quorum do banco e interromper decisões do cluster.
        </p>
      )}
      <div className="fault-buttons">
        <button
          className="danger"
          disabled={busy || !available}
          onClick={() => void apply("stop")}
        >
          {busy ? "Enviando…" : "Provocar falha"}
        </button>
        <button
          disabled={busy || !available}
          onClick={() => void apply("restore")}
        >
          Restaurar serviço
        </button>
      </div>
      {notice && (
        <p className="fault-notice" role="status">
          {notice}
        </p>
      )}
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      {recent.length > 0 && (
        <div className="fault-action-history">
          <h4>Ações recentes</h4>
          <ul>
            {recent.map((item) => (
              <li key={item.id}>
                <strong>{faultComponentNames[item.component]}</strong>{" "}
                <span>{faultActionStatusLabel(item.status)}</span>
                {item.error && <small>{item.error}</small>}
                {item.results && item.results.length > 0 && (
                  <ul className="fault-results">
                    {item.results.map((result) => (
                      <li key={`${item.id}-${result.component}`}>
                        {faultComponentNames[result.component]}:{" "}
                        {faultResultStatusLabel(result.status)}
                        {result.error ? ` · ${result.error}` : ""}
                      </li>
                    ))}
                  </ul>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  );
}
