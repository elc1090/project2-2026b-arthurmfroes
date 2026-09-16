import { api, APIError, message, sendPart } from "./api";
import { sameContent } from "./hash";
import type { Manifest, Operation } from "./types";
export type State =
  | "preparing"
  | "sending"
  | "recovering"
  | "confirming"
  | "complete"
  | "waiting"
  | "reselect"
  | "error"
  | "cancelled";
export const transferLabels: Record<State, string> = {
  preparing: "Preparando arquivo",
  sending: "Enviando",
  recovering: "Recuperando partes após falha de um node",
  confirming: "Confirmando armazenamento",
  complete: "Concluído",
  waiting: "Aguardando conexão ou verificação",
  reselect: "Selecione o arquivo novamente",
  error: "Não foi possível continuar",
  cancelled: "Cancelado",
};
export type Transfer = {
  key: string;
  name: string;
  size: number;
  folderID: string | null;
  state: State;
  progress: number;
  authoritativeProgress?: number;
  progressHighWater?: number;
  recoveryTarget?: number;
  error?: string;
  operation?: Operation;
  file?: File;
  manifest?: Manifest;
  abort: AbortController;
  running?: boolean;
  cancelling?: boolean;
  cancelRequested?: boolean;
};
export class Slots {
  private active = 0;
  private waiting: (() => void)[] = [];
  constructor(private limit = 2) {}
  async run<T>(task: () => Promise<T>): Promise<T> {
    if (this.active >= this.limit)
      await new Promise<void>((resolve) => this.waiting.push(resolve));
    else this.active++;
    try {
      return await task();
    } finally {
      const next = this.waiting.shift();
      if (next) next();
      else this.active--;
    }
  }
}

export function availableProgress(op: Operation): number {
  if (op.status === "available" || op.size === 0) return 1;
  return (
    op.parts.reduce((bytes, part) => bytes + (part.available ? part.size : 0), 0) /
    op.size
  );
}

export function missingParts(op: Operation) {
  return op.parts.filter((part) => !part.available);
}

export function recordUploadProgress(row: Transfer, progress: number) {
  row.progressHighWater = Math.max(row.progressHighWater || 0, progress);
  row.progress = row.progressHighWater;
  if (
    row.state === "recovering" &&
    row.recoveryTarget !== undefined &&
    progress >= row.recoveryTarget
  ) {
    row.state = "sending";
    row.recoveryTarget = undefined;
  }
}

export function reconcileTransfer(row: Transfer, op: Operation) {
  row.error = undefined;
  row.operation = op;
  const authoritative = availableProgress(op);
  const previousHighWater = row.progressHighWater ?? row.progress;
  row.authoritativeProgress = authoritative;
  row.progressHighWater = Math.max(previousHighWater, authoritative);
  row.progress = row.progressHighWater;
  const unavailable = missingParts(op);
  const recovering =
    op.status === "pending" &&
    !op.parts.some((part) => part.availability === "unknown") &&
    !!row.file &&
    unavailable.length > 0 &&
    authoritative < previousHighWater;
  if (recovering)
    row.recoveryTarget = Math.max(
      row.recoveryTarget || 0,
      previousHighWater,
    );
  if (
    row.recoveryTarget !== undefined &&
    authoritative >= row.recoveryTarget
  )
    row.recoveryTarget = undefined;

  row.state =
    op.status === "available"
      ? "complete"
      : op.status === "cancelled"
        ? "cancelled"
        : op.parts.some((part) => part.availability === "unknown")
          ? "waiting"
          : op.parts.every((part) => part.available)
            ? "confirming"
            : row.file
              ? row.recoveryTarget !== undefined &&
                authoritative < row.recoveryTarget
                ? "recovering"
                : "sending"
              : "reselect";
  if (
    op.status === "pending" &&
    op.error_code &&
    op.error_code !== "awaiting_parts"
  ) {
    const detail = operationError(op.error_code);
    row.error = detail.message;
    row.state = detail.transient ? "waiting" : "error";
  }
  if (["confirming", "complete", "cancelled"].includes(row.state))
    row.recoveryTarget = undefined;
  if (row.state === "complete" || row.state === "cancelled") {
    row.file = undefined;
    row.manifest = undefined;
  }
}

export class UploadQueue {
  rows: Transfer[] = [];
  private slots = new Slots(2);
  private hashSlots = new Slots(1);
  private disposed = false;
  private workers = new Set<Worker>();
  private storageKey: string;
  private lifetime = new AbortController();
  private tasks = new WeakMap<Transfer, Promise<void>>();
  private generations = new WeakMap<Transfer, number>();
  constructor(
    owner: string,
    private changed: () => void,
    private expired: () => void,
  ) {
    this.storageKey = `acervo:create:${owner}`;
  }
  private emit() {
    if (!this.disposed) this.changed();
  }
  private remember(key: string, remove = false) {
    try {
      let keys: string[] = [];
      try {
        const parsed: unknown = JSON.parse(
          localStorage.getItem(this.storageKey) || "[]",
        );
        if (Array.isArray(parsed))
          keys = parsed.filter(
            (value): value is string => typeof value === "string",
          );
      } catch {
        /* Backend discovery does not depend on readable local state. */
      }
      localStorage.setItem(
        this.storageKey,
        JSON.stringify(
          remove ? keys.filter((k) => k !== key) : [...new Set([...keys, key])],
        ),
      );
    } catch {
      if (!remove)
        throw new Error(
          "Não foi possível preservar a chave de criação no navegador. O envio não começou. Libere o armazenamento local e tente novamente.",
        );
    }
  }
  async restore() {
    if (this.disposed) return;
    let operations: Operation[] = [];
    let failures = 0;
    while (!this.disposed) {
      try {
        ({ operations } = await api<{ operations: Operation[] }>(
          "/uploads",
          "GET",
          undefined,
          this.lifetime.signal,
        ));
        break;
      } catch (error) {
        if (this.disposed) return;
        if (error instanceof APIError && error.status === 401) {
          this.expired();
          return;
        }
        const transient =
          error instanceof APIError &&
          (error.status === 0 || error.status >= 500);
        if (!transient) throw error;
        failures = Math.min(failures + 1, 4);
        await this.pause(
          this.lifetime.signal,
          Math.min(1000 * 2 ** failures, 16000),
        );
      }
    }
    if (this.disposed) return;
    for (const op of operations) {
      this.remember(op.idempotency_key, true);
      if (this.rows.some((r) => r.operation?.id === op.id)) continue;
      const row: Transfer = {
        key: op.idempotency_key,
        name: op.name,
        size: op.size,
        folderID: op.folder_id,
        operation: op,
        state: "waiting",
        progress: 0,
        abort: new AbortController(),
      };
      this.rows.push(row);
      this.apply(row, op);
      if (row.state === "confirming" || row.state === "waiting")
        void this.start(row);
    }
    this.emit();
  }
  add(files: File[], folderID: string | null) {
    if (this.disposed) return;
    for (const file of files) {
      const row: Transfer = {
        key: crypto.randomUUID(),
        name: file.name,
        size: file.size,
        folderID,
        file,
        state: "preparing",
        progress: 0,
        abort: new AbortController(),
      };
      this.rows.push(row);
      void this.start(row, file);
    }
    this.emit();
  }
  async reselect(row: Transfer, file: File) {
    if (
      this.disposed ||
      row.cancelRequested ||
      ["complete", "cancelled"].includes(row.state)
    )
      return;
    await this.start(row, file);
  }
  private start(row: Transfer, file?: File): Promise<void> {
    if (this.disposed || row.cancelling) return Promise.resolve();
    row.abort.abort();
    const generation = (this.generations.get(row) || 0) + 1;
    this.generations.set(row, generation);
    const previous = this.tasks.get(row);
    const next = (async () => {
      await previous;
      if (this.disposed || this.generations.get(row) !== generation) return;
      row.abort = new AbortController();
      row.error = undefined;
      if (file) await this.prepare(row, file);
      else await this.run(row);
    })();
    this.tasks.set(row, next);
    return next;
  }
  private async prepare(row: Transfer, file: File) {
    const signal = row.abort.signal;
    const uploadHighWater = row.progressHighWater || 0;
    row.state = "preparing";
    row.error = undefined;
    row.progress = 0;
    this.emit();
    try {
      const manifest = await this.hashSlots.run(
        () =>
          new Promise<Manifest>((resolve, reject) => {
            if (this.disposed || signal.aborted) {
              reject(new DOMException("Aborted", "AbortError"));
              return;
            }
            const worker = new Worker(
              new URL("./hash.worker.ts", import.meta.url),
              { type: "module" },
            );
            this.workers.add(worker);
            const finish = () => {
              worker.terminate();
              this.workers.delete(worker);
              signal.removeEventListener("abort", abort);
            };
            const abort = () => {
              finish();
              reject(new DOMException("Aborted", "AbortError"));
            };
            signal.addEventListener("abort", abort, { once: true });
            worker.onmessage = ({ data }) => {
              if (data.error) {
                finish();
                reject(new Error(data.error));
              } else if (data.manifest) {
                finish();
                resolve(data.manifest);
              } else {
                row.progress = file.size ? data.bytes / file.size : 1;
                this.emit();
              }
            };
            worker.onerror = () => {
              finish();
              reject(new Error("Não foi possível preparar o arquivo."));
            };
            worker.postMessage({ file });
          }),
      );
      if (this.disposed || signal.aborted) return;
      if (row.operation && !sameContent(manifest, row.operation))
        throw new Error(
          "O conteúdo selecionado é diferente. Selecione o arquivo original.",
        );
      row.file = file;
      row.manifest = manifest;
      row.progress = uploadHighWater;
      row.progressHighWater = uploadHighWater;
      await this.run(row);
    } catch (error) {
      if (!signal.aborted) {
        row.state = "error";
        row.error = message(error);
        this.emit();
      }
    }
  }
  private apply(row: Transfer, op: Operation) {
    if (this.disposed) return;
    reconcileTransfer(row, op);
    this.emit();
  }
  private async pause(signal: AbortSignal, ms: number) {
    if (signal.aborted || this.disposed) return;
    await new Promise<void>((resolve) => {
      const done = () => {
        clearTimeout(timer);
        signal.removeEventListener("abort", done);
        resolve();
      };
      const timer = setTimeout(done, ms);
      signal.addEventListener("abort", done, { once: true });
    });
  }
  private async run(row: Transfer) {
    if (row.running || this.disposed) return;
    row.running = true;
    const signal = row.abort.signal;
    let failures = 0;
    try {
      while (!this.disposed && !signal.aborted) {
        try {
          if (!row.operation) {
            if (!row.manifest) return;
            this.remember(row.key); // Persist before POST, including a response lost after commit.
            const op = await api<Operation>(
              "/uploads",
              "POST",
              {
                ...row.manifest,
                idempotency_key: row.key,
                folder_id: row.folderID,
                name: row.name,
              },
              signal,
            );
            if (signal.aborted || this.disposed) return;
            this.remember(row.key, true);
            this.apply(row, op);
          } else {
            const op = await api<Operation>(
              `/uploads/${row.operation.id}`,
              "GET",
              undefined,
              signal,
            );
            if (signal.aborted || this.disposed) return;
            this.apply(row, op);
          }
          if (
            ["complete", "cancelled", "reselect", "error"].includes(row.state)
          )
            return;
          if (row.state === "waiting") {
            await this.pause(row.abort.signal, 2000);
            continue;
          }
          const op = row.operation!;
          if (op.parts.some((p) => p.availability === "unknown")) {
            await this.pause(row.abort.signal, 2000);
            continue;
          }
          const missing = missingParts(op);
          if (missing.length && row.file) {
            // Reserve up to two parts, with one semaphore shared by every file.
            const confirmed = op.parts.reduce(
              (n, p) => n + (p.available ? p.size : 0),
              0,
            );
            const progress = new Map<number, number>();
            const results = await Promise.allSettled(
              missing.slice(0, 2).map((part) =>
                this.slots.run(async () => {
                  if (signal.aborted || this.disposed) return;
                  await sendPart(
                    op.id,
                    part.index,
                    row.file!.slice(part.offset, part.offset + part.size),
                    signal,
                    (bytes) => {
                      progress.set(part.index, bytes);
                      recordUploadProgress(
                        row,
                        (confirmed +
                          [...progress.values()].reduce((a, b) => a + b, 0)) /
                          op.size,
                      );
                      this.emit();
                    },
                  );
                }),
              ),
            );
            const failure = results.find((r) => r.status === "rejected");
            if (failure?.status === "rejected") throw failure.reason;
          } else await this.pause(row.abort.signal, 2000);
          failures = 0;
        } catch (error) {
          if (signal.aborted || this.disposed) return;
          if (error instanceof APIError && error.status === 401) {
            row.state = "waiting";
            row.error = message(error);
            this.emit();
            this.expired();
            return;
          }
          const transient =
            error instanceof APIError &&
            (error.status === 0 || error.status >= 500);
          if (!transient) {
            row.state = "error";
            row.error = message(error);
            this.emit();
            return;
          }
          failures = Math.min(failures + 1, 4);
          row.state = "waiting";
          row.error =
            "Aguardando conexão. Uma nova tentativa será feita automaticamente.";
          this.emit();
          await this.pause(row.abort.signal, Math.min(1000 * 2 ** failures, 16000));
          // The next loop queries authoritative parts before resending.
        }
      }
    } finally {
      row.running = false;
      this.emit();
    }
  }
  retry(row: Transfer) {
    if (
      this.disposed ||
      row.cancelling ||
      ["complete", "cancelled"].includes(row.state)
    )
      return;
    if (row.cancelRequested) {
      void this.cancel(row);
      return;
    }
    void this.start(
      row,
      !row.operation && !row.manifest ? row.file : undefined,
    );
  }
  removeCompletedFile(fileID: string) {
    if (this.disposed) return;
    const before = this.rows.length;
    this.rows = this.rows.filter(
      (row) =>
        !(
          row.state === "complete" && row.operation?.file_id === fileID
        ),
    );
    if (this.rows.length !== before) this.emit();
  }
  async cancel(row: Transfer) {
    if (
      this.disposed ||
      row.cancelling ||
      ["complete", "cancelled"].includes(row.state)
    )
      return;
    row.cancelRequested = true;
    row.cancelling = true;
    row.abort.abort();
    this.generations.set(row, (this.generations.get(row) || 0) + 1);
    this.emit();
    try {
      await this.tasks.get(row);
      if (this.disposed) return;
      // Resolve a lost creation response before cancelling the same operation.
      if (!row.operation && row.manifest) {
        this.remember(row.key);
        try {
          const op = await api<Operation>(
            "/uploads",
            "POST",
            {
              ...row.manifest,
              idempotency_key: row.key,
              folder_id: row.folderID,
              name: row.name,
            },
            this.lifetime.signal,
          );
          if (this.disposed) return;
          row.operation = op;
        } catch (error) {
          if (
            !(error instanceof APIError) ||
            ![400, 409].includes(error.status)
          )
            throw error;
          // A rejected creation cannot be cancelled remotely. Check whether the
          // key already belongs to an operation before dismissing this local row.
          const { operations } = await api<{ operations: Operation[] }>(
            "/uploads",
            "GET",
            undefined,
            this.lifetime.signal,
          );
          if (this.disposed) return;
          row.operation = operations.find(
            (op) => op.idempotency_key === row.key,
          );
        }
        this.remember(row.key, true);
      }
      if (row.operation) {
        const op = await api<Operation>(
          `/uploads/${row.operation.id}/cancel`,
          "POST",
          undefined,
          this.lifetime.signal,
        );
        if (this.disposed) return;
        this.apply(row, op);
      } else {
        row.state = "cancelled";
        row.file = undefined;
        row.manifest = undefined;
      }
    } catch (error) {
      if (this.disposed) return;
      row.state = "error";
      row.error = `Cancelamento ainda não confirmado. ${message(error)}`;
    } finally {
      row.cancelling = false;
      this.emit();
    }
  }
  dispose() {
    this.disposed = true;
    this.lifetime.abort();
    for (const row of this.rows) row.abort.abort();
    for (const worker of this.workers) worker.terminate();
    this.rows = [];
  }
}

export function operationError(code: string): {
  transient: boolean;
  message: string;
} {
  const transient = [
    "temporarily_unavailable",
    "node_unavailable",
    "authority_unavailable",
    "control_unavailable",
    "quorum_unavailable",
    "parts_unavailable",
    "awaiting_parts",
    "storage_unavailable",
  ].includes(code);
  const messages: Record<string, string> = {
    awaiting_parts: "Aguardando recuperação ou reenvio das partes faltantes.",
    content_mismatch:
      "A verificação de integridade falhou. Tente novamente ou cancele esta transferência.",
    hash_mismatch:
      "A verificação de integridade falhou. Tente novamente ou cancele esta transferência.",
    checksum_mismatch:
      "A verificação de integridade falhou. Tente novamente ou cancele esta transferência.",
    name_conflict:
      "Já existe um arquivo com esse nome nesta pasta. Cancele esta transferência e escolha outro nome.",
    insufficient_storage:
      "Não há espaço disponível no armazenamento. Libere capacidade antes de tentar novamente.",
    capacity_exceeded:
      "Não há espaço disponível no armazenamento. Libere capacidade antes de tentar novamente.",
  };
  return {
    transient,
    message:
      messages[code] ||
      (transient
        ? "Aguardando recuperação do serviço para verificar esta transferência."
        : "O armazenamento não conseguiu confirmar o arquivo. Tente novamente ou cancele esta transferência."),
  };
}
export function matchReselections(
  rows: Transfer[],
  files: File[],
): { matches: { row: Transfer; file: File }[]; errors: string[] } {
  const matches: { row: Transfer; file: File }[] = [],
    errors: string[] = [];
  const eligible = rows.filter(
    (row) =>
      ["reselect", "error"].includes(row.state) &&
      row.operation &&
      !row.cancelRequested,
  );
  for (const file of files) {
    const candidates = eligible.filter(
      (row) => row.name === file.name && row.size === file.size,
    );
    const duplicateSelections =
      files.filter(
        (other) => other.name === file.name && other.size === file.size,
      ).length > 1;
    if (candidates.length === 1 && !duplicateSelections)
      matches.push({ row: candidates[0], file });
    else if (candidates.length || duplicateSelections)
      errors.push(
        `Associação ambígua para ${file.name}. Use "Selecionar original" na linha desejada.`,
      );
    else errors.push(`Nenhuma transferência aguardando ${file.name}.`);
  }
  return { matches, errors: [...new Set(errors)] };
}
