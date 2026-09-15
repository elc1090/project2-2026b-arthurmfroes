import { test } from "node:test";
import assert from "node:assert/strict";
import { APIError } from "./api";
import { faultError, faultLabel, faultModes, setFault } from "./faults";
test("falha de qualquer componente ou total usa mesmo endpoint idempotente e restauração none", async () => {
  const original = globalThis.fetch;
  const requests: { path: string; mode: string }[] = [];
  globalThis.fetch = async (path, options) => {
    assert.equal(options?.method, "POST");
    requests.push({
      path: String(path),
      mode: JSON.parse(options?.body as string).mode,
    });
    return new Response(null, { status: 204 });
  };
  try {
    for (const mode of [...Object.keys(faultModes), "none"] as (
      keyof typeof faultModes | "none"
    )[])
      await setFault("manager/id", mode, new AbortController().signal);
    await setFault("manager/id", "none", new AbortController().signal);
    assert.equal(requests.length, 7);
    assert.ok(
      requests.every((r) => r.path === "/api/admin/nodes/manager%2Fid/fault"),
    );
    assert.deepEqual(
      requests.slice(-2).map((r) => r.mode),
      ["none", "none"],
    );
  } finally {
    globalThis.fetch = original;
  }
});
test("solicitação rejeitada ou resposta perdida não vira confirmação de estado", async () => {
  const original = globalThis.fetch;
  globalThis.fetch = async () => {
    throw new TypeError("response lost");
  };
  try {
    await assert.rejects(setFault("id", "total", new AbortController().signal));
    assert.match(faultError(new TypeError()), /Não foi possível confirmar/);
    assert.match(faultError(new APIError(404, "disabled")), /indisponível/);
    assert.match(faultError(new APIError(403, "forbidden")), /permissão/);
    assert.equal(faultLabel("none"), "Nenhuma falha simulada");
    assert.equal(faultLabel("total"), "Falha simulada: Nó inteiro");
  } finally {
    globalThis.fetch = original;
  }
});
