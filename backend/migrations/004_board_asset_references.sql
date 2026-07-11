ALTER TABLE boards
  ADD CONSTRAINT boards_id_project_id_key UNIQUE (id, project_id);

ALTER TABLE assets
  ADD CONSTRAINT assets_id_project_id_key UNIQUE (id, project_id);

CREATE TABLE board_asset_reference_state (
  board_id TEXT PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
  indexed_through_sequence BIGINT NOT NULL DEFAULT -1 CHECK (indexed_through_sequence >= -1),
  refs_hash TEXT NOT NULL DEFAULT '' CHECK (refs_hash = '' OR refs_hash ~ '^[0-9a-f]{64}$'),
  indexed_by TEXT REFERENCES users(id) ON DELETE SET NULL,
  indexed_at TIMESTAMPTZ,
  conflicted BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE TABLE board_asset_references (
  board_id TEXT NOT NULL,
  project_id TEXT NOT NULL,
  asset_id TEXT NOT NULL,
  PRIMARY KEY (board_id, asset_id),
  FOREIGN KEY (board_id, project_id)
    REFERENCES boards(id, project_id) ON DELETE CASCADE,
  FOREIGN KEY (asset_id, project_id)
    REFERENCES assets(id, project_id) ON DELETE NO ACTION
);

CREATE INDEX board_asset_references_asset_idx
  ON board_asset_references (project_id, asset_id, board_id);

-- Existing documents are opaque to the migration and therefore start stale.
INSERT INTO board_asset_reference_state (board_id)
SELECT id FROM boards
ON CONFLICT (board_id) DO NOTHING;
