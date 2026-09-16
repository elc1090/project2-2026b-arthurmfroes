CREATE TABLE file_deletions (
 file_id UUID PRIMARY KEY,
 operation_id UUID NOT NULL UNIQUE,
 owner_id UUID NOT NULL,
 deleted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 FOREIGN KEY (operation_id,owner_id) REFERENCES upload_operations(id,owner_id),
 INDEX file_deletions_owner (owner_id,deleted_at,file_id)
);
