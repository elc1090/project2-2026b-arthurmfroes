import { useEffect, useRef, useState } from "react";
import { APIError } from "./api";
import {
  faultError,
  faultLabel,
  faultModes,
  setFault,
  type FaultMode,
} from "./faults";
export function FaultControls({
  id,
  nodeID,
  observed,
  manager,
  refresh,
  expired,
}: {
  id: string;
  nodeID: string;
  observed: FaultMode;
  manager: boolean;
  refresh: () => void;
  expired: () => void;
}) {
  const [mode, select] = useState<FaultMode>("storage"),
    [busy, setBusy] = useState(false),
    [notice, setNotice] = useState(""),
    [error, setError] = useState("");
  const last = useRef<FaultMode>("storage"),
    mounted = useRef(true),
    request = useRef<AbortController | null>(null);
  useEffect(
    () => () => {
      mounted.current = false;
      request.current?.abort();
    },
    [],
  );
  async function apply(value: FaultMode) {
    if (request.current) return;
    const controller = new AbortController();
    request.current = controller;
    last.current = value;
    setBusy(true);
    setNotice("");
    setError("");
    const timer = setTimeout(() => controller.abort(), 10000);
    try {
      await setFault(id, value, controller.signal);
      if (!mounted.current) return;
      setNotice(
        value === "none"
          ? "Restauração solicitada. O nó precisa recuperar a saúde e sincronizar antes de voltar ao atendimento."
          : "Falha solicitada. Aguarde a observação do gerenciador para acompanhar a retirada do nó.",
      );
      refresh();
    } catch (e) {
      if (!mounted.current) return;
      if (e instanceof APIError && e.status === 401) expired();
      else {
        setError(faultError(e));
        refresh();
      }
    } finally {
      clearTimeout(timer);
      request.current = null;
      if (mounted.current) setBusy(false);
    }
  }
  return (
    <section className="fault-controls" aria-label={`Simulação em ${nodeID}`}>
      <h4>Simulação de desenvolvimento</h4>
      <p className="simulation-observed">{faultLabel(observed)}</p>
      {manager && (
        <p>
          Este nó executa o gerenciador. Uma falha total também interrompe essa
          atividade.
        </p>
      )}
      <label>
        Componente em {nodeID}
        <select
          value={mode}
          onChange={(e) => select(e.target.value as FaultMode)}
          disabled={busy}
        >
          {Object.entries(faultModes).map(([value, label]) => (
            <option value={value} key={value}>
              {label}
            </option>
          ))}
        </select>
      </label>
      <div className="fault-buttons">
        <button disabled={busy} onClick={() => void apply(mode)}>
          {busy ? "Enviando…" : "Provocar falha"}
        </button>
        <button disabled={busy} onClick={() => void apply("none")}>
          Restaurar nó
        </button>
      </div>
      {notice && (
        <p className="fault-notice" role="status">
          {notice}
        </p>
      )}
      {error && (
        <>
          <p className="error" role="alert">
            {error}
          </p>
          <button disabled={busy} onClick={() => void apply(last.current)}>
            Repetir solicitação
          </button>
        </>
      )}
    </section>
  );
}
