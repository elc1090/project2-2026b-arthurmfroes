import { useEffect, useRef, useState } from "react";
import { api, APIError } from "./api";
import type { AdminNode } from "./admin-model";
import {
  executeNodeCommand,
  canCancelNodeOperation,
  cancelNodeOperation,
  nodeCommandError,
  nodeOperationError,
  nodeOperationKinds,
  operationStages,
  PendingCommands,
  type NodeCommand,
  type NodeOperation,
} from "./node-commands";
export function NodeManagement({
  owner,
  nodes,
  expired,
  refresh,
}: {
  owner: string;
  nodes: AdminNode[];
  expired: () => void;
  refresh: () => void;
}) {
  const [operations, setOperations] = useState<NodeOperation[]>([]),
    [error, setError] = useState(""),
    [pollError, setPollError] = useState(""),
    [notice, setNotice] = useState(""),
    [busy, setBusy] = useState(false),
    [retire, setRetire] = useState(""),
    [confirm, setConfirm] = useState(false),
    [showForm, setShowForm] = useState(false),
    [pendingRows, setPendingRows] = useState<NodeCommand[]>([]);
  const store = useRef<PendingCommands | null>(null);
  if (!store.current) store.current = new PendingCommands(owner);
  const lifetime = useRef(new AbortController()),
    action = useRef<AbortController | null>(null),
    pollNow = useRef<() => void>(() => {}),
    callbacks = useRef({ expired, refresh });
  callbacks.current = { expired, refresh };
  useEffect(() => {
    setPendingRows(store.current!.list());
    let stopped = false,
      inFlight = false,
      timer: ReturnType<typeof setTimeout>;
    let request: AbortController | undefined;
    async function poll() {
      if (stopped || inFlight) return;
      clearTimeout(timer);
      inFlight = true;
      request = new AbortController();
      const timeout = setTimeout(() => request?.abort(), 8000);
      try {
        const result = await api<{ operations: NodeOperation[] }>(
          "/admin/node-operations",
          "GET",
          undefined,
          request.signal,
        );
        if (stopped) return;
        setOperations(result.operations);
        setPollError("");
        for (const op of result.operations)
          if (op.idempotency_key) store.current!.remove(op.idempotency_key);
        setPendingRows(store.current!.list());
      } catch (e) {
        if (stopped) return;
        if (e instanceof APIError && e.status === 401)
          callbacks.current.expired();
        else
          setPollError(
            "Não foi possível atualizar as etapas. Os últimos dados podem estar desatualizados.",
          );
      } finally {
        clearTimeout(timeout);
        inFlight = false;
        if (!stopped) timer = setTimeout(poll, 5000);
      }
    }
    pollNow.current = () => void poll();
    void poll();
    return () => {
      stopped = true;
      clearTimeout(timer);
      request?.abort();
      lifetime.current.abort();
      action.current?.abort();
      pollNow.current = () => {};
    };
  }, []);
  async function send(command: NodeCommand) {
    if (action.current || lifetime.current.signal.aborted) return;
    const controller = new AbortController();
    action.current = controller;
    setBusy(true);
    setError("");
    setNotice("");
    const timeout = setTimeout(() => controller.abort(), 10000);
    try {
      const result = await executeNodeCommand(
        command,
        store.current!,
        controller.signal,
      );
      if (lifetime.current.signal.aborted) return;
      setOperations((old) => [
        result,
        ...old.filter((op) => op.id !== result.id),
      ]);
      setNotice(
        "Solicitação recebida. Acompanhe as etapas abaixo; o estado do nó é decidido pelo gerenciador.",
      );
      setConfirm(false);
      setShowForm(false);
      pollNow.current();
      callbacks.current.refresh();
    } catch (e) {
      if (lifetime.current.signal.aborted) return;
      if (e instanceof APIError && e.status === 401)
        callbacks.current.expired();
      else setError(nodeCommandError(e));
    } finally {
      clearTimeout(timeout);
      action.current = null;
      if (!lifetime.current.signal.aborted) {
        setBusy(false);
        setPendingRows(store.current!.list());
      }
    }
  }
  async function cancel(op: NodeOperation) {
    if (action.current || lifetime.current.signal.aborted) return;
    const controller = new AbortController();
    action.current = controller;
    setBusy(true);
    setError("");
    const timeout = setTimeout(() => controller.abort(), 10000);
    try {
      await cancelNodeOperation(op, controller.signal);
      if (lifetime.current.signal.aborted) return;
      setNotice(
        "Cancelamento solicitado antes das etapas externas. Consulte o resultado nas operações.",
      );
      pollNow.current();
    } catch (error) {
      if (!lifetime.current.signal.aborted) setError(nodeCommandError(error));
    } finally {
      clearTimeout(timeout);
      action.current = null;
      if (!lifetime.current.signal.aborted) setBusy(false);
    }
  }
  return (
    <section className="node-management">
      <div className="transfer-heading">
        <h2>Associação e retirada</h2>
        <button onClick={() => setShowForm(!showForm)}>
          Registrar nó provisionado
        </button>
      </div>
      <p className="admin-note">
        Estes comandos configuram serviços já provisionados. Eles não criam
        máquinas. A retirada preserva os volumes e verifica as condições do
        cluster.
      </p>
      {showForm && (
        <form
          className="registration-form"
          onSubmit={(e) => {
            e.preventDefault();
            const data = new FormData(e.currentTarget);
            const registration = {
              node_id: String(data.get("node_id")).trim(),
              backend_endpoint: String(data.get("backend_endpoint")).trim(),
              database_endpoint: String(data.get("database_endpoint")).trim(),
              storage_endpoint: String(data.get("storage_endpoint")).trim(),
              credential_profile: "default" as const,
            };
            void send({
              kind: "join",
              node_id: registration.node_id,
              registration,
              idempotency_key: crypto.randomUUID(),
            });
          }}
        >
          <label>
            Identidade do nó
            <input
              name="node_id"
              required
              placeholder="node-4"
              disabled={busy}
            />
          </label>
          <label>
            Endpoint do backend
            <input
              name="backend_endpoint"
              type="url"
              required
              placeholder="http://backend:8080"
              disabled={busy}
            />
          </label>
          <label>
            Endpoint do banco
            <input
              name="database_endpoint"
              required
              placeholder="postgresql://banco:26257/acervo"
              disabled={busy}
            />
          </label>
          <label>
            Endpoint do armazenamento
            <input
              name="storage_endpoint"
              type="url"
              required
              placeholder="http://storage:9000"
              disabled={busy}
            />
          </label>
          <p className="admin-note">
            Perfil de credenciais: default, configurado no servidor. Não informe
            senhas nos endpoints.
          </p>
          <button className="primary" disabled={busy}>
            Solicitar associação
          </button>
        </form>
      )}
      <div className="retire-form">
        <label>
          Nó para retirada permanente
          <select
            value={retire}
            onChange={(e) => {
              setRetire(e.target.value);
              setConfirm(false);
            }}
            disabled={busy}
          >
            <option value="">Selecione um nó</option>
            {nodes
              .filter((n) => n.state !== "removed")
              .map((n) => (
                <option key={n.id} value={n.id}>
                  {n.node_id}
                </option>
              ))}
          </select>
        </label>
        <button disabled={!retire || busy} onClick={() => setConfirm(true)}>
          Solicitar retirada
        </button>
      </div>
      {confirm && (
        <div className="retire-confirm">
          <p>
            Retirar {nodes.find((n) => n.id === retire)?.node_id} da composição
            do cluster? O gerenciador verifica quorum e topologia antes de
            executar as etapas. Os volumes são preservados.
          </p>
          <button
            disabled={busy}
            onClick={() =>
              void send({
                kind: "retire",
                node_id: retire,
                idempotency_key: crypto.randomUUID(),
              })
            }
          >
            Confirmar solicitação de retirada
          </button>
          <button disabled={busy} onClick={() => setConfirm(false)}>
            Voltar
          </button>
        </div>
      )}
      {notice && <p role="status">{notice}</p>}
      {pollError && (
        <p className="error" role="status">
          {pollError}
        </p>
      )}
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      {pendingRows.length > 0 && (
        <div className="pending-commands">
          <h3>Solicitações sem resposta confirmada</h3>
          {pendingRows.map((command) => (
            <div key={command.idempotency_key}>
              <span>
                {command.kind === "join" ? "Associar" : "Retirar"}{" "}
                {command.node_id}
              </span>
              <button disabled={busy} onClick={() => void send(command)}>
                Repetir mesma solicitação
              </button>
            </div>
          ))}
        </div>
      )}
      <div className="admin-table-wrap">
        <table className="admin-table">
          <thead>
            <tr>
              <th>Operação</th>
              <th>Nó</th>
              <th>Etapa</th>
              <th>Mandato</th>
              <th>Impedimento</th>
            </tr>
          </thead>
          <tbody>
            {operations.map((op) => (
              <tr key={op.id}>
                <td>
                  {nodeOperationKinds[op.kind] || "Operação desconhecida"}
                  <br />
                  <code>{op.id}</code>
                </td>
                <td>
                  {nodes.find((n) => n.id === op.node_id)?.node_id ||
                    op.node_id}
                </td>
                <td>{operationStages[op.stage] || "Etapa desconhecida"}</td>
                <td>{op.manager_term}</td>
                <td>
                  {nodeOperationError(op.last_error) ||
                    "Nenhum impedimento registrado"}
                  {canCancelNodeOperation(op) && (
                    <div>
                      <button disabled={busy} onClick={() => void cancel(op)}>
                        Cancelar intenção de retirada
                      </button>
                    </div>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {operations.length === 0 && (
          <p className="admin-empty">
            Nenhuma operação de associação ou retirada nesta consulta.
          </p>
        )}
      </div>
    </section>
  );
}
