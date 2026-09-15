import { test } from "node:test";
import assert from "node:assert/strict";
import { APIError } from "./api";
import {
  cancelNodeOperation,
  canCancelNodeOperation,
  executeNodeCommand,
  nodeCommandError,
  PendingCommands,
  type NodeCommand,
  type NodeOperation,
} from "./node-commands";
const memory = () => {
  const values = new Map<string, string>();
  return {
    getItem: (key: string) => values.get(key) || null,
    setItem: (key: string, value: string) => {
      values.set(key, value);
    },
  };
};
const command: NodeCommand = {
  kind: "join",
  idempotency_key: "stable",
  node_id: "node-4",
  registration: {
    node_id: "node-4",
    backend_endpoint: "http://node4:8080",
    database_endpoint: "postgresql://node4:26257/acervo",
    storage_endpoint: "http://storage4:9000",
    credential_profile: "default",
  },
};
test("resposta perdida preserva comando por conta e retomada repete chave e endpoints", async () => {
  const storage = memory(),
    pending = new PendingCommands("admin-a", storage),
    original = globalThis.fetch;
  const bodies: unknown[] = [];
  globalThis.fetch = async (_url, options) => {
    bodies.push(JSON.parse(options?.body as string));
    assert.equal(pending.list()[0].idempotency_key, "stable");
    if (bodies.length === 1) throw new TypeError("response lost");
    return new Response(
      JSON.stringify({
        id: "op",
        node_id: "node-4",
        kind: "join",
        stage: "preflight",
        manager_term: 1,
        last_error: null,
      }),
      { status: 202 },
    );
  };
  try {
    await assert.rejects(
      executeNodeCommand(command, pending, new AbortController().signal),
    );
    assert.equal(new PendingCommands("admin-b", storage).list().length, 0);
    const resumed = new PendingCommands("admin-a", storage);
    await executeNodeCommand(
      resumed.list()[0],
      resumed,
      new AbortController().signal,
    );
    assert.deepEqual(bodies[0], bodies[1]);
    assert.equal(resumed.list().length, 0);
  } finally {
    globalThis.fetch = original;
  }
});
test("senhas em endpoints não são persistidas no navegador", async () => {
  const pending = new PendingCommands("owner", memory());
  await assert.rejects(
    executeNodeCommand(
      {
        ...command,
        registration: {
          ...command.registration!,
          database_endpoint: "postgresql://root:secret@db:26257/acervo",
        },
      },
      pending,
      new AbortController().signal,
    ),
  );
  assert.equal(pending.list().length, 0);
});
test("cancelamento administrativo só permite retirada em preflight e informa bloqueio de topologia", async () => {
  const original = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = async () => {
    calls++;
    return new Response(null, { status: 204 });
  };
  const op: NodeOperation = {
    id: "op",
    node_id: "node",
    kind: "retire",
    stage: "preflight",
    manager_term: 1,
    last_error: null,
  };
  try {
    assert.equal(canCancelNodeOperation(op), true);
    await cancelNodeOperation(op, new AbortController().signal);
    await assert.rejects(
      cancelNodeOperation(
        { ...op, stage: "sql" },
        new AbortController().signal,
      ),
    );
    await assert.rejects(
      cancelNodeOperation(
        { ...op, kind: "join" },
        new AbortController().signal,
      ),
    );
    assert.equal(calls, 1);
    assert.match(
      nodeCommandError(new APIError(409, "cockroach_topology_blocked")),
      /três réplicas SQL/,
    );
  } finally {
    globalThis.fetch = original;
  }
});

test("JSON malformado, formato inesperado e leitura proibida não derrubam painel", () => {
  for (const value of [
    "{broken",
    "null",
    "{}",
    '[null,{}, {"kind":"replace"}]',
  ]) {
    assert.deepEqual(
      new PendingCommands("owner", {
        getItem: () => value,
        setItem: () => {},
      }).list(),
      [],
    );
  }
  assert.deepEqual(
    new PendingCommands("owner", {
      getItem: () => {
        throw new DOMException("blocked", "SecurityError");
      },
      setItem: () => {},
    }).list(),
    [],
  );
});
test("falha de escrita impede POST e endpoints com userinfo ou query não são persistidos", async () => {
  const original = globalThis.fetch;
  let requests = 0;
  globalThis.fetch = async () => {
    requests++;
    return new Response("{}");
  };
  try {
    const blocked = new PendingCommands("owner", {
      getItem: () => null,
      setItem: () => {
        throw new DOMException("quota", "QuotaExceededError");
      },
    });
    await assert.rejects(
      executeNodeCommand(command, blocked, new AbortController().signal),
    );
    for (const endpoint of [
      "http://user@host:9000",
      "http://host:9000?token=secret",
      "http://user:pass@host:9000",
    ]) {
      const pending = new PendingCommands("owner", memory());
      await assert.rejects(
        executeNodeCommand(
          {
            ...command,
            registration: {
              ...command.registration!,
              storage_endpoint: endpoint,
            },
          },
          pending,
          new AbortController().signal,
        ),
      );
      assert.deepEqual(pending.list(), []);
    }
    assert.equal(requests, 0);
  } finally {
    globalThis.fetch = original;
  }
});
test("recuperação automática remove associação antiga sem oferecer cancelamento", () => {
  const op: NodeOperation = {
    id: "replace",
    node_id: "node",
    kind: "replace",
    stage: "remove_old",
    manager_term: 4,
    last_error: null,
  };
  assert.equal(canCancelNodeOperation(op), false);
  assert.equal(canCancelNodeOperation({ ...op, stage: "preflight" }), false);
});
