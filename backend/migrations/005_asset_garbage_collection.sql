ALTER TABLE board_updates
  ADD COLUMN introduced_asset_ids TEXT[];

CREATE TABLE asset_gc_candidates (
  asset_id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  detected_at TIMESTAMPTZ NOT NULL,
  eligible_at TIMESTAMPTZ NOT NULL,
  FOREIGN KEY (asset_id, project_id)
    REFERENCES assets(id, project_id) ON DELETE CASCADE,
  CHECK (eligible_at >= detected_at)
);

CREATE INDEX asset_gc_candidates_due_idx
  ON asset_gc_candidates (eligible_at, asset_id);

CREATE INDEX asset_gc_candidates_project_idx
  ON asset_gc_candidates (project_id, asset_id);

CREATE INDEX boards_project_id_idx
  ON boards (project_id, id);
