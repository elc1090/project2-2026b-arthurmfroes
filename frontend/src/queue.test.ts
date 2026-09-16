import { test } from "node:test";
import assert from "node:assert/strict";
import {
  missingParts,
  reconcileTransfer,
  recordUploadProgress,
  Slots,
  transferLabels,
  UploadQueue,
  type Transfer,
} from "./queue";
import type { Operation } from "./types";
test("concorrência global dois slots e erro isolado libera lugar na fila", async () => {
  const slots = new Slots(2);
  let active = 0,
    peak = 0,
    finished = 0;
  const results = await Promise.allSettled(
    Array.from({ length: 10 }, (_, i) =>
      slots.run(async () => {
        active++;
        peak = Math.max(peak, active);
        await new Promise((r) => setTimeout(r, 2));
        active--;
        if (i === 2) throw new Error("isolado");
        finished++;
      }),
    ),
  );
  assert.equal(peak, 2);
  assert.equal(finished, 9);
  assert.equal(results.filter((r) => r.status === "rejected").length, 1);
});
test("reabertura consulta backend sem estado local e limpa dados ao sair", async () => {
  const stored = new Map<string, string>();
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: {
      getItem: (key: string) => stored.get(key) || null,
      setItem: (key: string, value: string) => stored.set(key, value),
    },
  });
  const original = globalThis.fetch;
  const op: Operation = {
    id: "operation",
    idempotency_key: "key",
    name: "private.bin",
    folder_id: null,
    size: 3,
    sha256: "abc",
    status: "pending",
    phase: "receiving",
    parts: [
      {
        index: 0,
        offset: 0,
        size: 3,
        sha256: "abc",
        available: false,
        availability: "missing",
      },
    ],
  };
  globalThis.fetch = async () =>
    new Response(
      JSON.stringify({
        operations: [
          op,
          {
            ...op,
            id: "published",
            idempotency_key: "published-key",
            status: "available",
            phase: "complete",
            file_id: "file",
          },
        ],
      }),
    );
  const queue = new UploadQueue(
    "owner",
    () => {},
    () => {},
  );
  try {
    await queue.restore();
    assert.equal(queue.rows[0].state, "reselect");
    assert.equal(queue.rows[1].state, "complete");
    assert.equal(queue.rows[1].file, undefined);
    assert.equal(queue.rows[1].progress, 1);
    queue.dispose();
    assert.deepEqual(queue.rows, []);
  } finally {
    globalThis.fetch = original;
    queue.dispose();
  }
});
test("perda da resposta de criação repete a mesma chave antes de enviar e só publicação conclui", async () => {
  const stored = new Map<string, string>();
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: {
      getItem: (key: string) => stored.get(key) || null,
      setItem: (key: string, value: string) => stored.set(key, value),
    },
  });
  const originalFetch = globalThis.fetch,
    originalTimeout = globalThis.setTimeout;
  // Deterministic retry delay: no request relies on elapsed real seconds in this contract test.
  globalThis.setTimeout = ((fn: () => void) =>
    originalTimeout(fn, 0)) as typeof setTimeout;
  let creates = 0,
    queries = 0;
  const keys: string[] = [];
  const seen: string[] = [];
  const op: Operation = {
    id: "empty",
    idempotency_key: "stable",
    name: "empty",
    folder_id: null,
    size: 0,
    sha256: "hash",
    parts: [],
    status: "pending",
    phase: "confirming",
  };
  globalThis.fetch = async (_url, options) => {
    if (options?.method === "POST") {
      creates++;
      const body = JSON.parse(options.body as string);
      keys.push(body.idempotency_key);
      assert.ok(stored.get("acervo:create:owner")!.includes("stable"));
      if (creates === 1) throw new TypeError("response lost");
      return new Response(JSON.stringify(op));
    }
    queries++;
    return new Response(
      JSON.stringify({
        ...op,
        status: "available",
        phase: "complete",
        file_id: "final",
      }),
    );
  };
  const queue = new UploadQueue(
    "owner",
    () => seen.push(queue.rows[0]?.state),
    () => {},
  );
  queue.rows.push({
    key: "stable",
    name: "empty",
    size: 0,
    folderID: null,
    state: "error",
    progress: 0,
    manifest: { size: 0, sha256: "hash", parts: [] },
    abort: new AbortController(),
  });
  try {
    queue.retry(queue.rows[0]);
    for (let i = 0; i < 50 && String(queue.rows[0].state) !== "complete"; i++)
      await new Promise((r) => originalTimeout(r, 2));
    assert.deepEqual(keys, ["stable", "stable"]);
    assert.equal(queries, 1);
    assert.ok(seen.includes("waiting"));
    assert.ok(seen.includes("confirming"));
    assert.equal(queue.rows[0].state, "complete");
  } finally {
    queue.dispose();
    globalThis.fetch = originalFetch;
    globalThis.setTimeout = originalTimeout;
  }
});
test("disponibilidade desconhecida espera verificação sem solicitar seleção ou enviar bytes", async () => {
  const originalFetch = globalThis.fetch;
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: { getItem: () => null, setItem: () => {} },
  });
  const op: Operation = {
    id: "unknown",
    idempotency_key: "key",
    name: "data",
    folder_id: null,
    size: 1,
    sha256: "hash",
    parts: [
      {
        index: 0,
        offset: 0,
        size: 1,
        sha256: "hash",
        availability: "unknown",
        available: false,
      },
    ],
    status: "pending",
    phase: "receiving",
  };
  const methods: string[] = [];
  globalThis.fetch = async (url, options) => {
    methods.push(options?.method || "GET");
    return new Response(
      JSON.stringify(
        String(url).endsWith("/uploads") ? { operations: [op] } : op,
      ),
    );
  };
  const queue = new UploadQueue(
    "owner",
    () => {},
    () => {},
  );
  try {
    await queue.restore();
    await new Promise((r) => setTimeout(r, 5));
    assert.equal(queue.rows[0].state, "waiting");
    assert.ok(methods.every((m) => m === "GET"));
  } finally {
    queue.dispose();
    globalThis.fetch = originalFetch;
  }
});

const pendingOperation = (patch: Partial<Operation> = {}): Operation => ({
  id: "op",
  idempotency_key: "key",
  name: "file",
  folder_id: null,
  size: 1,
  sha256: "hash",
  parts: [
    {
      index: 0,
      offset: 0,
      size: 1,
      sha256: "hash",
      available: false,
      availability: "missing",
    },
  ],
  status: "pending",
  phase: "receiving",
  ...patch,
});
function memoryStorage() {
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: { getItem: () => null, setItem: () => {} },
  });
}
const tick = () => new Promise<void>((resolve) => setTimeout(resolve, 0));

test("erro operacional interrompe confirmação e retry imediato aguarda execução anterior", async () => {
  memoryStorage();
  const original = globalThis.fetch;
  let reads = 0,
    retried = false;
  const op = pendingOperation({ parts: [], size: 0, phase: "confirming" });
  globalThis.fetch = async (url) => {
    if (String(url).endsWith("/uploads"))
      return new Response(
        JSON.stringify({
          operations: [{ ...op, error_code: "content_mismatch" }],
        }),
      );
    reads++;
    return new Response(
      JSON.stringify(
        reads === 1
          ? { ...op, error_code: "name_conflict" }
          : { ...op, status: "available", phase: "complete", file_id: "file" },
      ),
    );
  };
  const queue = new UploadQueue(
    "owner",
    () => {
      const row = queue.rows[0];
      if (row?.error?.includes("Já existe") && !retried) {
        retried = true;
        assert.equal(row.running, true);
        queue.retry(row);
      }
    },
    () => {},
  );
  try {
    await queue.restore();
    assert.equal(queue.rows[0].state, "error");
    assert.match(queue.rows[0].error!, /integridade/);
    queue.retry(queue.rows[0]);
    for (let i = 0; i < 20 && String(queue.rows[0].state) !== "complete"; i++)
      await tick();
    assert.equal(queue.rows[0].state, "complete");
    assert.equal(reads, 2);
    assert.equal(queue.rows[0].error, undefined);
  } finally {
    queue.dispose();
    globalThis.fetch = original;
  }
});

test("cancelar durante GET ignora resposta atrasada; retry imediato não reinicia envio", async () => {
  memoryStorage();
  const original = globalThis.fetch;
  const op = pendingOperation();
  let resolveGet!: (response: Response) => void;
  let cancelCalls = 0;
  const paths: string[] = [];
  globalThis.fetch = async (url) => {
    const path = String(url);
    paths.push(path);
    if (path.endsWith("/uploads"))
      return new Response(JSON.stringify({ operations: [op] }));
    if (path.endsWith("/cancel")) {
      cancelCalls++;
      return new Response(
        JSON.stringify({ ...op, status: "cancelled", phase: "cancelled" }),
      );
    }
    return new Promise((resolve) => {
      resolveGet = resolve;
    });
  };
  const queue = new UploadQueue(
    "owner",
    () => {},
    () => {},
  );
  try {
    await queue.restore();
    const row = queue.rows[0];
    queue.retry(row);
    await tick();
    assert.equal(row.running, true);
    const cancellation = queue.cancel(row);
    queue.retry(row);
    resolveGet(
      new Response(
        JSON.stringify({ ...op, parts: [], size: 0, phase: "confirming" }),
      ),
    );
    await cancellation;
    await tick();
    assert.equal(row.state, "cancelled");
    assert.equal(row.running, false);
    assert.equal(cancelCalls, 1);
    assert.equal(paths.length, 3);
  } finally {
    queue.dispose();
    globalThis.fetch = original;
  }
});

test("falha no cancelamento preserva intenção e retry repete cancelamento", async () => {
  memoryStorage();
  const original = globalThis.fetch;
  const op = pendingOperation();
  let cancelled = 0;
  globalThis.fetch = async (url) => {
    if (String(url).endsWith("/uploads"))
      return new Response(JSON.stringify({ operations: [op] }));
    assert.ok(String(url).endsWith("/cancel"));
    cancelled++;
    if (cancelled === 1)
      return new Response('{"error":"unavailable"}', { status: 503 });
    return new Response(
      JSON.stringify({ ...op, status: "cancelled", phase: "cancelled" }),
    );
  };
  const queue = new UploadQueue(
    "owner",
    () => {},
    () => {},
  );
  try {
    await queue.restore();
    const row = queue.rows[0];
    await queue.cancel(row);
    assert.equal(row.state, "error");
    queue.retry(row);
    await tick();
    assert.equal(row.state, "cancelled");
    assert.equal(cancelled, 2);
  } finally {
    queue.dispose();
    globalThis.fetch = original;
  }
});

test("logout durante restore ou cancel aborta requisição e ignora resposta tardia", async () => {
  memoryStorage();
  const original = globalThis.fetch;
  const op = pendingOperation();
  let release!: (response: Response) => void;
  let signal: AbortSignal | undefined;
  let events = 0;
  globalThis.fetch = async (_url, options) => {
    signal = options?.signal as AbortSignal;
    return new Promise((resolve) => {
      release = resolve;
    });
  };
  const first = new UploadQueue(
    "owner",
    () => events++,
    () => {},
  );
  try {
    const restore = first.restore();
    first.dispose();
    assert.equal(signal?.aborted, true);
    release(new Response(JSON.stringify({ operations: [op] })));
    await restore;
    assert.equal(first.rows.length, 0);
    assert.equal(events, 0);
    globalThis.fetch = async () =>
      new Response(JSON.stringify({ operations: [op] }));
    const second = new UploadQueue(
      "owner",
      () => events++,
      () => {},
    );
    await second.restore();
    globalThis.fetch = async (_url, options) => {
      signal = options?.signal as AbortSignal;
      return new Promise((resolve) => {
        release = resolve;
      });
    };
    const cancellation = second.cancel(second.rows[0]);
    await tick();
    const before = events;
    second.dispose();
    assert.equal(signal?.aborted, true);
    release(
      new Response(
        JSON.stringify({ ...op, status: "cancelled", phase: "cancelled" }),
      ),
    );
    await cancellation;
    assert.equal(second.rows.length, 0);
    assert.equal(events, before);
  } finally {
    first.dispose();
    globalThis.fetch = original;
  }
});

test("seleção múltipla associa candidatas únicas e rejeita ambiguidades sem enviar", async () => {
  const { matchReselections, operationError } = await import("./queue");
  const row = (key: string, name: string) => ({
    key,
    name,
    size: 1,
    folderID: null,
    state: "reselect" as const,
    progress: 0,
    operation: pendingOperation(),
    abort: new AbortController(),
  });
  const rows = [row("a", "a"), row("b", "b"), row("c1", "c"), row("c2", "c")];
  const result = matchReselections(rows, [
    new File(["1"], "a"),
    new File(["2"], "b"),
    new File(["3"], "c"),
  ]);
  assert.deepEqual(
    result.matches.map((m) => m.row.key),
    ["a", "b"],
  );
  assert.equal(result.errors.length, 1);
  assert.equal(
    matchReselections(rows, [new File(["1"], "a"), new File(["2"], "a")])
      .matches.length,
    0,
  );
  assert.equal(operationError("storage_unavailable").transient, true);
  assert.equal(operationError("capacity_exceeded").transient, false);
  assert.equal(
    operationError("SQL password=secret").message.includes("secret"),
    false,
  );
});

test("cancelar criação rejeitada verifica chave e encerra linha local sem excluir arquivo existente", async () => {
  memoryStorage();
  const original = globalThis.fetch;
  const paths: string[] = [];
  globalThis.fetch = async (url, options) => {
    paths.push(`${options?.method} ${url}`);
    return options?.method === "POST"
      ? new Response('{"error":"conflict"}', { status: 409 })
      : new Response(
          JSON.stringify({
            operations: [
              pendingOperation({
                idempotency_key: "another-key",
                status: "available",
              }),
            ],
          }),
        );
  };
  const queue = new UploadQueue(
    "owner",
    () => {},
    () => {},
  );
  queue.rows.push({
    key: "new-key",
    name: "file",
    size: 1,
    folderID: null,
    state: "error",
    progress: 0,
    manifest: { size: 1, sha256: "hash", parts: [] },
    abort: new AbortController(),
  });
  try {
    await queue.cancel(queue.rows[0]);
    assert.equal(queue.rows[0].state, "cancelled");
    assert.equal(queue.rows[0].manifest, undefined);
    assert.deepEqual(paths, ["POST /api/uploads", "GET /api/uploads"]);
  } finally {
    queue.dispose();
    globalThis.fetch = original;
  }
});

test("sete falhas transitórias recuperam automaticamente com espera limitada a 16 segundos", async () => {
  memoryStorage();
  const originalFetch = globalThis.fetch,
    originalTimeout = globalThis.setTimeout;
  const delays: number[] = [];
  globalThis.setTimeout = ((fn: () => void, ms: number) => {
    delays.push(ms);
    return originalTimeout(fn, 0);
  }) as typeof setTimeout;
  let attempts = 0;
  const op = pendingOperation();
  globalThis.fetch = async (url) => {
    if (String(url).endsWith("/uploads"))
      return new Response(JSON.stringify({ operations: [op] }));
    attempts++;
    return attempts <= 7
      ? new Response("{}", { status: 503 })
      : new Response(
          JSON.stringify({ ...op, status: "available", phase: "complete" }),
        );
  };
  const queue = new UploadQueue(
    "owner",
    () => {},
    () => {},
  );
  try {
    await queue.restore();
    queue.retry(queue.rows[0]);
    for (let i = 0; i < 100 && queue.rows[0].state !== "complete"; i++)
      await new Promise((r) => originalTimeout(r, 2));
    assert.equal(attempts, 8);
    assert.equal(queue.rows[0].state, "complete");
    assert.deepEqual(delays, [2000, 4000, 8000, 16000, 16000, 16000, 16000]);
  } finally {
    queue.dispose();
    globalThis.fetch = originalFetch;
    globalThis.setTimeout = originalTimeout;
  }
});

test("cancelamento e logout interrompem espera prolongada sem outra consulta", async () => {
  for (const dispose of [false, true]) {
    memoryStorage();
    const original = globalThis.fetch;
    let queries = 0;
    const op = pendingOperation();
    globalThis.fetch = async (url) => {
      if (String(url).endsWith("/uploads"))
        return new Response(JSON.stringify({ operations: [op] }));
      if (String(url).endsWith("/cancel"))
        return new Response(
          JSON.stringify({ ...op, status: "cancelled", phase: "cancelled" }),
        );
      queries++;
      return new Response("{}", { status: 503 });
    };
    const queue = new UploadQueue(
      "owner",
      () => {},
      () => {},
    );
    try {
      await queue.restore();
      const row = queue.rows[0];
      queue.retry(row);
      await tick();
      assert.equal(row.state, "waiting");
      if (dispose) queue.dispose();
      else await queue.cancel(row);
      await tick();
      assert.equal(queries, 1);
      assert.equal(row.running, false);
      if (!dispose) assert.equal(row.state, "cancelled");
    } finally {
      queue.dispose();
      globalThis.fetch = original;
    }
  }
});

test("restore independe de JSON local válido e de armazenamento local permitido", async () => {
  const original = globalThis.fetch;
  globalThis.fetch = async () =>
    new Response(JSON.stringify({ operations: [pendingOperation()] }));
  try {
    for (const value of ["{invalid", "{}", "null"]) {
      Object.defineProperty(globalThis, "localStorage", {
        configurable: true,
        value: {
          getItem: () => value,
          setItem: () => {
            throw new Error("blocked");
          },
        },
      });
      const queue = new UploadQueue(
        "owner",
        () => {},
        () => {},
      );
      await queue.restore();
      assert.equal(queue.rows[0].state, "reselect");
      queue.dispose();
    }
  } finally {
    globalThis.fetch = original;
  }
});

test("criação sem persistência e erro de JSON são definitivos, sem loop de rede", async () => {
  const original = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = async () => {
    calls++;
    return new Response("{invalid");
  };
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: {
      getItem: () => null,
      setItem: () => {
        throw new Error("quota");
      },
    },
  });
  const queue = new UploadQueue(
    "owner",
    () => {},
    () => {},
  );
  queue.rows.push({
    key: "key",
    name: "file",
    size: 0,
    folderID: null,
    state: "error",
    progress: 0,
    manifest: { size: 0, sha256: "hash", parts: [] },
    abort: new AbortController(),
  });
  try {
    queue.retry(queue.rows[0]);
    await tick();
    assert.equal(calls, 0);
    assert.equal(queue.rows[0].state, "error");
    assert.match(queue.rows[0].error!, /chave de criação/);
    memoryStorage();
    queue.rows[0].operation = pendingOperation();
    queue.retry(queue.rows[0]);
    await tick();
    assert.equal(calls, 1);
    assert.equal(queue.rows[0].state, "error");
  } finally {
    queue.dispose();
    globalThis.fetch = original;
  }
});

test("erro HTTP definitivo não é repetido como falha de rede", async () => {
  memoryStorage();
  const original = globalThis.fetch;
  let queries = 0;
  const op = pendingOperation();
  globalThis.fetch = async (url) => {
    if (String(url).endsWith("/uploads"))
      return new Response(JSON.stringify({ operations: [op] }));
    queries++;
    return new Response('{"error":"conflict"}', { status: 409 });
  };
  const queue = new UploadQueue(
    "owner",
    () => {},
    () => {},
  );
  try {
    await queue.restore();
    queue.retry(queue.rows[0]);
    await tick();
    assert.equal(queries, 1);
    assert.equal(queue.rows[0].state, "error");
    assert.equal(queue.rows[0].running, false);
  } finally {
    queue.dispose();
    globalThis.fetch = original;
  }
});

test("restore repete rede e 503 além de quatro falhas e recupera IDs sem nova criação", async () => {
  memoryStorage();
  const originalFetch = globalThis.fetch, originalTimeout = globalThis.setTimeout;
  const delays: number[] = [];
  let calls = 0;
  globalThis.setTimeout = ((fn: () => void, ms: number) => {
    delays.push(ms);
    return originalTimeout(fn, 0);
  }) as typeof setTimeout;
  globalThis.fetch = async (_url, options) => {
    assert.equal(options?.method, "GET");
    calls++;
    if (calls === 1) throw new TypeError("Failed to fetch");
    if (calls <= 6) return new Response("{}", {status: 503});
    return new Response(JSON.stringify({operations: [pendingOperation()]}));
  };
  const queue = new UploadQueue("owner", () => {}, () => {});
  try {
    await queue.restore();
    assert.equal(calls, 7);
    assert.equal(queue.rows[0].operation?.id, pendingOperation().id);
    assert.equal(queue.rows[0].state, "reselect");
    assert.deepEqual(delays, [2000,4000,8000,16000,16000,16000]);
  } finally {
    queue.dispose();
    globalThis.fetch = originalFetch;
    globalThis.setTimeout = originalTimeout;
  }
});

test("dispose interrompe espera de restore sem consultar novamente", async () => {
  const original = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = async () => { calls++; return new Response("{}", {status: 503}); };
  const queue = new UploadQueue("owner", () => {}, () => {});
  try {
    const restoring = queue.restore();
    await tick();
    queue.dispose();
    await restoring;
    assert.equal(calls, 1);
    assert.deepEqual(queue.rows, []);
  } finally { queue.dispose(); globalThis.fetch = original; }
});

test("restore encerra sessão no 401 e expõe 4xx ou JSON inválido sem retry", async () => {
  const original = globalThis.fetch;
  try {
    for (const status of [401, 403, 409, 200]) {
      let calls = 0, expired = 0;
      globalThis.fetch = async () => {
        calls++;
        return new Response(status === 200 ? "{invalid" : "{}", {status});
      };
      const queue = new UploadQueue("owner", () => {}, () => {expired++;});
      try {
        if (status === 401) await queue.restore();
        else await assert.rejects(queue.restore());
        assert.equal(calls, 1);
        assert.equal(expired, status === 401 ? 1 : 0);
        assert.deepEqual(queue.rows, []);
      } finally { queue.dispose(); }
    }
  } finally { globalThis.fetch = original; }
});

test("progresso visual não regride quando uma parte disponível se perde", () => {
  const part = (index: number, available: boolean) => ({
    index,
    offset: index * 25,
    size: 25,
    sha256: `hash-${index}`,
    available,
    availability: available ? ("available" as const) : ("missing" as const),
  });
  const operation = (available: boolean[]): Operation => ({
    id: "recovering",
    idempotency_key: "key",
    folder_id: null,
    name: "large.bin",
    size: 100,
    sha256: "hash",
    status: "pending",
    phase: "receiving",
    parts: available.map((value, index) => part(index, value)),
  });
  const row: Transfer = {
    key: "key",
    name: "large.bin",
    size: 100,
    folderID: null,
    state: "sending",
    progress: 0,
    file: new File([new Uint8Array(100)], "large.bin"),
    abort: new AbortController(),
  };

  reconcileTransfer(row, operation([true, true, true, false]));
  assert.equal(row.progress, 0.75);
  assert.equal(row.authoritativeProgress, 0.75);
  assert.deepEqual(missingParts(row.operation!).map((item) => item.index), [3]);

  reconcileTransfer(row, operation([true, false, true, false]));
  assert.equal(row.authoritativeProgress, 0.5);
  assert.equal(row.progress, 0.75);
  assert.equal(row.state, "recovering");
  assert.equal(
    transferLabels[row.state],
    "Recuperando partes após falha de um node",
  );
  assert.deepEqual(missingParts(row.operation!).map((item) => item.index), [1, 3]);

  recordUploadProgress(row, 0.7);
  assert.equal(row.progress, 0.75);
  assert.equal(row.state, "recovering");
  recordUploadProgress(row, 0.75);
  assert.equal(row.progress, 0.75);
  assert.equal(row.state, "sending");

  reconcileTransfer(row, {
    ...operation([true, true, true, true]),
    phase: "confirming",
  });
  assert.equal(row.state, "confirming");
  assert.equal(row.progress, 1);
  reconcileTransfer(row, {
    ...operation([true, true, true, true]),
    status: "available",
    phase: "complete",
    file_id: "file-1",
  });
  assert.equal(row.state, "complete");
});

test("exclusão retira somente a transferência concluída e reload respeita fila vazia", async () => {
  memoryStorage();
  const original = globalThis.fetch;
  let changes = 0;
  const complete = pendingOperation({
    id: "published",
    status: "available",
    phase: "complete",
    file_id: "file-1",
  });
  const queue = new UploadQueue("owner", () => changes++, () => {});
  queue.rows.push({
    key: "published",
    name: "published.bin",
    size: 1,
    folderID: null,
    state: "complete",
    progress: 1,
    operation: complete,
    abort: new AbortController(),
  });
  queue.rows.push({
    key: "pending",
    name: "pending.bin",
    size: 1,
    folderID: null,
    state: "sending",
    progress: 0,
    operation: pendingOperation({ id: "pending", file_id: "file-1" }),
    abort: new AbortController(),
  });
  try {
    queue.removeCompletedFile("file-1");
    assert.deepEqual(queue.rows.map((row) => row.key), ["pending"]);
    assert.equal(changes, 1);
    queue.dispose();

    globalThis.fetch = async () =>
      Response.json({ operations: [] });
    const reloaded = new UploadQueue("owner", () => {}, () => {});
    await reloaded.restore();
    assert.deepEqual(reloaded.rows, []);
    reloaded.dispose();
  } finally {
    queue.dispose();
    globalThis.fetch = original;
  }
});
