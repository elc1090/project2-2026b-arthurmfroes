import { test } from "node:test";
import assert from "node:assert/strict";
import { APIError } from "./api";
import {
  confirmFaultAction,
  faultConfirmation,
  faultError,
  requestFaultAction,
} from "./faults";

test("ação usa endpoint único com alvo tipado", async () => {
  const original = globalThis.fetch;
  const requests: { path: string; body: unknown }[] = [];
  globalThis.fetch = async (path, options) => {
    requests.push({
      path: String(path),
      body: JSON.parse(options?.body as string),
    });
    return Response.json({
      id: "action-1",
      node_id: "node/internal",
      component: "storage",
      action: "stop",
      status: "requested",
      error: null,
      updated_at: "2026-09-16T12:00:00Z",
    });
  };
  try {
    const result = await requestFaultAction(
      "node/internal",
      "storage",
      "stop",
      new AbortController().signal,
    );
    assert.equal(result.status, "requested");
    assert.deepEqual(requests, [
      {
        path: "/api/admin/fault-actions",
        body: {
          node_id: "node/internal",
          component: "storage",
          action: "stop",
        },
      },
    ]);
  } finally {
    globalThis.fetch = original;
  }
});

test("cancelamento da confirmação não envia requisição", async () => {
  const original = globalThis.fetch;
  let fetched = false;
  globalThis.fetch = async () => {
    fetched = true;
    return new Response(null, { status: 500 });
  };
  try {
    const result = await confirmFaultAction(
      () => false,
      "id-2",
      "node-2",
      "node",
      "Nó inteiro",
      "stop",
      new AbortController().signal,
    );
    assert.equal(result, null);
    assert.equal(fetched, false);
    assert.match(
      faultConfirmation("node-2", "Banco local", "sql", "stop"),
      /quorum/,
    );
    assert.doesNotMatch(
      faultConfirmation("node-2", "Backend", "backend", "stop"),
      /quorum/,
    );
  } finally {
    globalThis.fetch = original;
  }
});

test("erros administrativos não exibem payload do provedor", () => {
  assert.match(faultError(new APIError(403, "secret")), /permissão/);
  assert.match(faultError(new APIError(404, "secret")), /cadastrado/);
  assert.match(faultError(new APIError(503, "ssh private key")), /indisponível/);
  assert.doesNotMatch(faultError(new Error("password=secret")), /secret/);
});
