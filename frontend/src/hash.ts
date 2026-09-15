import { sha256 } from "@noble/hashes/sha2.js";
import { bytesToHex } from "@noble/hashes/utils.js";
import type { Manifest, Part } from "./types";
export const PART_SIZE = 32 * 1024 * 1024;
export function ranges(
  size: number,
  partSize = PART_SIZE,
): Omit<Part, "sha256">[] {
  if (
    !Number.isSafeInteger(size) ||
    size < 0 ||
    !Number.isSafeInteger(partSize) ||
    partSize < 1
  )
    throw new Error("Tamanho inválido.");
  const parts = [];
  for (let offset = 0, index = 0; offset < size; offset += partSize, index++)
    parts.push({ index, offset, size: Math.min(partSize, size - offset) });
  return parts;
}
// Only one slice is materialized at a time. File bytes never cross back to the UI.
export async function prepare(
  file: Blob,
  progress: (bytes: number) => void = () => {},
  partSize = PART_SIZE,
): Promise<Manifest> {
  const full = sha256.create();
  const parts: Part[] = [];
  for (const part of ranges(file.size, partSize)) {
    const bytes = new Uint8Array(
      await file.slice(part.offset, part.offset + part.size).arrayBuffer(),
    );
    full.update(bytes);
    parts.push({ ...part, sha256: bytesToHex(sha256(bytes)) });
    progress(part.offset + part.size);
  }
  return { size: file.size, sha256: bytesToHex(full.digest()), parts };
}
export function sameContent(a: Manifest, b: Manifest): boolean {
  return (
    a.size === b.size &&
    a.sha256 === b.sha256 &&
    a.parts.length === b.parts.length &&
    a.parts.every((p, i) => {
      const q = b.parts[i];
      return (
        p.index === q.index &&
        p.offset === q.offset &&
        p.size === q.size &&
        p.sha256 === q.sha256
      );
    })
  );
}
