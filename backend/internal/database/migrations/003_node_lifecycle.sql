CREATE TABLE node_lifecycle_operations (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 idempotency_key STRING NOT NULL UNIQUE,
 node_id UUID NOT NULL REFERENCES cluster_nodes(id),
 kind STRING NOT NULL CHECK (kind IN ('join','retire','replace')),
 stage STRING NOT NULL CHECK (stage IN ('preflight','sql','remove_old','storage','complete','cancelled')),
 request JSONB NOT NULL,
 manager_term INT8 NOT NULL,
 last_error STRING,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 topology_slot BOOL NOT NULL DEFAULT true CHECK(topology_slot)
);
CREATE UNIQUE INDEX one_topology_operation ON node_lifecycle_operations(topology_slot) WHERE stage NOT IN ('complete','cancelled');
CREATE TABLE node_infrastructure (
 node_id UUID PRIMARY KEY REFERENCES cluster_nodes(id),
 identity JSONB NOT NULL,
 blocked BOOL NOT NULL
);

CREATE TABLE node_storage_replacements (
 node_id UUID PRIMARY KEY REFERENCES cluster_nodes(id),
 previous_identity JSONB NOT NULL,
 observed_identity JSONB NOT NULL,
 storage_generation UUID NOT NULL,
 operation_id UUID NULL REFERENCES node_lifecycle_operations(id),
 complete BOOL NOT NULL DEFAULT false,
 observed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE node_storage_tombstones (
 node_id UUID NOT NULL REFERENCES cluster_nodes(id),
 deployment_id STRING NOT NULL,
 retired_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(node_id,deployment_id)
);
