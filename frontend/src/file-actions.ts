import { api } from "./api";
import type { DriveFile } from "./types";

export function fileDeletionConfirmation(file: DriveFile): string {
  return `Excluir permanentemente “${file.name}”? Esta ação não pode ser desfeita.`;
}

export async function deleteFile(
  fileID: string,
  signal: AbortSignal,
): Promise<void> {
  await api(`/files/${encodeURIComponent(fileID)}`, "DELETE", undefined, signal);
}

export async function confirmFileDeletion(
  file: DriveFile,
  confirm: (message: string) => boolean,
  signal: AbortSignal,
  accepted: () => void = () => {},
): Promise<boolean> {
  if (!confirm(fileDeletionConfirmation(file))) return false;
  accepted();
  await deleteFile(file.id, signal);
  return true;
}
