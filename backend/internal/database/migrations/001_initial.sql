CREATE TABLE users (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 login STRING NOT NULL UNIQUE CHECK (length(trim(login))>0),
 password_hash STRING NOT NULL CHECK (length(password_hash)>0),
 is_admin BOOL NOT NULL DEFAULT false,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE sessions (
 token_hash BYTES PRIMARY KEY CHECK (length(token_hash)=32),
 user_id UUID NOT NULL REFERENCES users(id),
 expires_at TIMESTAMPTZ NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK (expires_at>created_at),
 INDEX sessions_user (user_id)
);
CREATE TABLE folders (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 owner_id UUID NOT NULL REFERENCES users(id),
 parent_id UUID NULL,
 name STRING NOT NULL CHECK (length(name)>0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE (id,owner_id),
 FOREIGN KEY (parent_id,owner_id) REFERENCES folders(id,owner_id),
 CHECK (parent_id IS NULL OR parent_id<>id),
 UNIQUE INDEX folders_nested_name (owner_id,parent_id,name) WHERE parent_id IS NOT NULL,
 UNIQUE INDEX folders_root_name (owner_id,name) WHERE parent_id IS NULL
);
CREATE TABLE cluster_nodes (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 node_id STRING NOT NULL UNIQUE CHECK (length(trim(node_id))>0),
 backend_endpoint STRING NOT NULL,
 database_endpoint STRING NOT NULL,
 storage_endpoint STRING NOT NULL,
 storage_generation UUID NOT NULL DEFAULT gen_random_uuid(),
 state STRING NOT NULL DEFAULT 'joining' CHECK (state IN ('joining','syncing','ready','unavailable','removed')),
 health JSONB NOT NULL DEFAULT '{}'::JSONB,
 reason STRING NULL,
 observed_at TIMESTAMPTZ NULL,
 transitioned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 synced_publication_generation INT8 NOT NULL DEFAULT 0 CHECK (synced_publication_generation>=0)
);
CREATE TABLE cluster_configuration (
 singleton BOOL PRIMARY KEY DEFAULT true CHECK (singleton),
 version INT8 NOT NULL DEFAULT 0 CHECK (version>=0),
 publication_generation INT8 NOT NULL DEFAULT 0 CHECK (publication_generation>=0),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE cluster_membership (
 node_id UUID PRIMARY KEY REFERENCES cluster_nodes(id),
 configuration BOOL NOT NULL DEFAULT true REFERENCES cluster_configuration(singleton) CHECK (configuration)
);
CREATE TABLE manager_lease (
 singleton BOOL PRIMARY KEY DEFAULT true CHECK (singleton),
 holder_id UUID NULL REFERENCES cluster_nodes(id),
 term INT8 NOT NULL DEFAULT 0 CHECK (term>=0),
 expires_at TIMESTAMPTZ NULL,
 CHECK ((holder_id IS NULL)=(expires_at IS NULL))
);
CREATE TABLE upload_operations (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 owner_id UUID NOT NULL REFERENCES users(id),
 folder_id UUID NULL,
 name STRING NOT NULL CHECK (length(name)>0),
 idempotency_key UUID NOT NULL,
 size_bytes INT8 NOT NULL CHECK (size_bytes>=0),
 sha256 BYTES NOT NULL CHECK (length(sha256)=32),
 manifest JSONB NOT NULL CHECK (jsonb_typeof(manifest)='array'),
 part_count INT8 NOT NULL CHECK (part_count>=0),
 status STRING NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','available','cancelled')),
 phase STRING NOT NULL DEFAULT 'receiving' CHECK (phase IN ('receiving','confirming','awaiting_parts','complete','cancelled')),
 configuration_version INT8 NOT NULL DEFAULT 0 CHECK (configuration_version>=0),
 version INT8 NOT NULL DEFAULT 0 CHECK (version>=0),
 lease_holder UUID NULL REFERENCES cluster_nodes(id),
 lease_generation INT8 NOT NULL DEFAULT 0 CHECK (lease_generation>=0),
 lease_expires_at TIMESTAMPTZ NULL,
 error_code STRING NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(owner_id,idempotency_key),
 UNIQUE(id,owner_id),
 UNIQUE(id,size_bytes,sha256),
 FOREIGN KEY(folder_id,owner_id) REFERENCES folders(id,owner_id),
 CHECK ((lease_holder IS NULL)=(lease_expires_at IS NULL)),
 CHECK ((size_bytes=0)=(part_count=0)),
 CHECK ((status='pending' AND phase IN ('receiving','confirming','awaiting_parts')) OR (status='available' AND phase='complete') OR (status='cancelled' AND phase='cancelled')),
 INDEX upload_owner_state (owner_id,status)
);
CREATE TABLE upload_parts (
 operation_id UUID NOT NULL REFERENCES upload_operations(id),
 part_index INT8 NOT NULL CHECK (part_index>=0),
 offset_bytes INT8 NOT NULL CHECK (offset_bytes>=0),
 size_bytes INT8 NOT NULL CHECK (size_bytes>0),
 sha256 BYTES NOT NULL CHECK (length(sha256)=32),
 PRIMARY KEY(operation_id,part_index),
 UNIQUE(operation_id,offset_bytes),
 UNIQUE(operation_id,part_index,size_bytes,sha256)
);
CREATE TABLE upload_part_copies (
 operation_id UUID NOT NULL,
 part_index INT8 NOT NULL,
 node_id UUID NOT NULL REFERENCES cluster_nodes(id),
 storage_generation UUID NOT NULL,
 object_key STRING NOT NULL CHECK (length(object_key)>0),
 s3_version_id STRING NOT NULL CHECK (length(s3_version_id)>0),
 size_bytes INT8 NOT NULL,
 sha256 BYTES NOT NULL,
 verified_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(operation_id,part_index,node_id,storage_generation),
 FOREIGN KEY(operation_id,part_index,size_bytes,sha256) REFERENCES upload_parts(operation_id,part_index,size_bytes,sha256)
);
CREATE TABLE object_copies (
 operation_id UUID NOT NULL,
 node_id UUID NOT NULL REFERENCES cluster_nodes(id),
 storage_generation UUID NOT NULL,
 object_key STRING NOT NULL CHECK (length(object_key)>0),
 s3_version_id STRING NOT NULL CHECK (length(s3_version_id)>0),
 size_bytes INT8 NOT NULL,
 sha256 BYTES NOT NULL,
 verified_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(operation_id,node_id,storage_generation),
 FOREIGN KEY(operation_id,size_bytes,sha256) REFERENCES upload_operations(id,size_bytes,sha256)
);
CREATE TABLE files (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 operation_id UUID NOT NULL UNIQUE,
 owner_id UUID NOT NULL REFERENCES users(id),
 folder_id UUID NULL,
 name STRING NOT NULL CHECK (length(name)>0),
 published_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 configuration_version INT8 NOT NULL CHECK (configuration_version>=0),
 FOREIGN KEY(operation_id,owner_id) REFERENCES upload_operations(id,owner_id),
 FOREIGN KEY(folder_id,owner_id) REFERENCES folders(id,owner_id),
 UNIQUE INDEX files_nested_name (owner_id,folder_id,name) WHERE folder_id IS NOT NULL,
 UNIQUE INDEX files_root_name (owner_id,name) WHERE folder_id IS NULL
);
CREATE TABLE cluster_events (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 node_id UUID NULL REFERENCES cluster_nodes(id),
 configuration_version INT8 NOT NULL CHECK (configuration_version>=0),
 manager_term INT8 NOT NULL CHECK (manager_term>=0),
 kind STRING NOT NULL CHECK (length(kind)>0),
 details JSONB NOT NULL DEFAULT '{}'::JSONB,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 INDEX cluster_events_time(created_at)
);
INSERT INTO cluster_configuration(singleton) VALUES(true);
INSERT INTO manager_lease(singleton) VALUES(true);
