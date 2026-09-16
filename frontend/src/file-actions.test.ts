import { test } from "node:test";
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { APIError } from "./api";
import { FileActions } from "./FileActions";
import {
  confirmFileDeletion,
  fileDeletionConfirmation,
} from "./file-actions";
import type { DriveFile } from "./types";

const file: DriveFile = {
  id: "file/1",
  name: "trabalho final.pdf",
  size: 42,
  published_at: "2026-09-16T12:00:00Z",
};

test("cancelar confirmação não envia exclusão", async () => {
  const original = globalThis.fetch;
  let calls = 0,
    accepted = 0;
  globalThis.fetch = async () => {
    calls++;
    return new Response(null, { status: 204 });
  };
  try {
    const result = await confirmFileDeletion(
      file,
      () => false,
      new AbortController().signal,
      () => accepted++,
    );
    assert.equal(result, false);
    assert.equal(calls, 0);
    assert.equal(accepted, 0);
    assert.match(fileDeletionConfirmation(file), /não pode ser desfeita/);
  } finally {
    globalThis.fetch = original;
  }
});

test("confirmação envia DELETE com identificador escapado", async () => {
  const original = globalThis.fetch;
  const requests: { path: string; method: string | undefined }[] = [];
  globalThis.fetch = async (path, options) => {
    requests.push({ path: String(path), method: options?.method });
    return new Response(null, { status: 204 });
  };
  let accepted = 0;
  try {
    const result = await confirmFileDeletion(
      file,
      () => true,
      new AbortController().signal,
      () => accepted++,
    );
    assert.equal(result, true);
    assert.equal(accepted, 1);
    assert.deepEqual(requests, [
      { path: "/api/files/file%2F1", method: "DELETE" },
    ]);
  } finally {
    globalThis.fetch = original;
  }
});

test("erro da exclusão permanece disponível para a interface", async () => {
  const original = globalThis.fetch;
  globalThis.fetch = async () =>
    Response.json({ error: "temporarily_unavailable" }, { status: 503 });
  try {
    await assert.rejects(
      confirmFileDeletion(
        file,
        () => true,
        new AbortController().signal,
      ),
      (error) => error instanceof APIError && error.status === 503,
    );
  } finally {
    globalThis.fetch = original;
  }
});

test("listagem apresenta download e exclusão permanente com estado ocupado", () => {
  const html = renderToStaticMarkup(
    createElement(FileActions, {
      file,
      deleting: true,
      deletionPending: true,
      onDelete: () => {},
    }),
  );
  assert.match(html, /href="\/api\/files\/file%2F1\/download"/);
  assert.match(html, /Excluir permanentemente trabalho final.pdf/);
  assert.match(html, /disabled=""/);
  assert.match(html, /Excluindo…/);
});
