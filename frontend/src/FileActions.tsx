import type { DriveFile } from "./types";

export function FileActions({
  file,
  deleting,
  deletionPending,
  onDelete,
}: {
  file: DriveFile;
  deleting: boolean;
  deletionPending: boolean;
  onDelete: () => void;
}) {
  return (
    <span className="file-actions">
      <a
        href={`/api/files/${encodeURIComponent(file.id)}/download`}
        download={file.name}
        aria-label={`Baixar ${file.name}`}
      >
        ↓ Baixar
      </a>
      <button
        className="delete-file"
        disabled={deletionPending}
        onClick={onDelete}
        aria-label={`Excluir permanentemente ${file.name}`}
      >
        {deleting ? "Excluindo…" : "Excluir"}
      </button>
    </span>
  );
}
