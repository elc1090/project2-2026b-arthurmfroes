export type User = { id: string; login: string; is_admin: boolean };
export type Folder = {
  id: string;
  parent_id: string | null;
  name: string;
  created_at: string;
};
export type DriveFile = {
  id: string;
  name: string;
  size: number;
  published_at: string;
};
export type Listing = {
  folder: Folder | null;
  folders: Folder[];
  files: DriveFile[];
};
export type Part = {
  index: number;
  offset: number;
  size: number;
  sha256: string;
};
export type Manifest = { size: number; sha256: string; parts: Part[] };
export type Operation = Omit<Manifest, "parts"> & {
  id: string;
  idempotency_key: string;
  folder_id: string | null;
  name: string;
  parts: (Part & {
    available: boolean;
    availability: "available" | "missing" | "unknown";
  })[];
  status: "pending" | "available" | "cancelled";
  phase:
    "receiving" | "confirming" | "awaiting_parts" | "complete" | "cancelled";
  file_id?: string;
  error_code?: string;
};
