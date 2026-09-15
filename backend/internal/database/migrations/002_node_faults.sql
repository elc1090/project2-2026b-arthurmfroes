CREATE TABLE node_faults (
 node_id UUID PRIMARY KEY REFERENCES cluster_nodes(id),
 mode STRING NOT NULL CHECK (mode IN ('none','backend','sql','storage','control','total')),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
