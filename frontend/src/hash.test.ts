import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { prepare, ranges, sameContent, PART_SIZE } from "./hash";
test("SHA-256 conhecido, vazio e divisão final menor", async () => {
  const empty = await prepare(new Blob([]));
  assert.equal(
    empty.sha256,
    "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
  );
  assert.deepEqual(empty.parts, []);
  const abc = await prepare(new Blob(["abc"]), () => {}, 2);
  assert.equal(
    abc.sha256,
    "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
  );
  assert.deepEqual(
    abc.parts.map((p) => [p.index, p.offset, p.size]),
    [
      [0, 0, 2],
      [1, 2, 1],
    ],
  );
  assert.equal(
    abc.parts[0].sha256,
    createHash("sha256").update("ab").digest("hex"),
  );
});
test("rejeita mesmo tamanho e nome com bytes diferentes, e manifesto alterado", async () => {
  const a = await prepare(new File(["abc"], "igual.bin")),
    b = await prepare(new File(["abd"], "igual.bin"));
  assert.equal(sameContent(a, b), false);
  assert.equal(sameContent(a, a), true);
  assert.equal(
    sameContent(a, { ...a, parts: [{ ...a.parts[0], offset: 1 }] }),
    false,
  );
});
test("offsets acima de 2 GiB sem teto de referência e aritmética segura", () => {
  const size = 5 * 1024 ** 3 + 17;
  const parts = ranges(size);
  assert.equal(parts.length, 161);
  assert.equal(parts.at(-1)!.offset, 5 * 1024 ** 3);
  assert.equal(parts.at(-1)!.size, 17);
  assert.equal(ranges(2 * 1024 ** 3).length, 64);
  assert.throws(() => ranges(Number.MAX_SAFE_INTEGER + 1));
});
test("leitura limitada a uma parte por vez, sem arrayBuffer do arquivo inteiro", async () => {
  const data = Buffer.alloc(7 * 1024 ** 2 + 11, 42);
  let active = 0,
    peak = 0,
    maxRead = 0;
  const source = {
    size: data.length,
    arrayBuffer() {
      throw new Error("whole file forbidden");
    },
    slice(start: number, end: number) {
      maxRead = Math.max(maxRead, end - start);
      return {
        async arrayBuffer() {
          active++;
          peak = Math.max(peak, active);
          await Promise.resolve();
          const result = Uint8Array.from(data.subarray(start, end)).buffer;
          active--;
          return result;
        },
      };
    },
  } as unknown as Blob;
  const result = await prepare(source, () => {}, 1024 ** 2);
  assert.equal(result.sha256, createHash("sha256").update(data).digest("hex"));
  assert.equal(peak, 1);
  assert.equal(maxRead, 1024 ** 2);
  assert.equal(PART_SIZE, 32 * 1024 ** 2);
});
