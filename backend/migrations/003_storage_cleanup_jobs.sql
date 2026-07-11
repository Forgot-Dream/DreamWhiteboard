CREATE TABLE storage_cleanup_jobs (
  id BIGSERIAL PRIMARY KEY,
  kind TEXT NOT NULL CHECK (kind IN ('asset_file', 'project_dir')),
  project_id TEXT NOT NULL CHECK (project_id <> ''),
  target_key TEXT NOT NULL DEFAULT '',
  attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
  available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  lease_token TEXT,
  lease_until TIMESTAMPTZ,
  last_error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (
    (kind = 'asset_file' AND target_key <> '')
    OR (kind = 'project_dir' AND target_key = '')
  ),
  CHECK ((lease_token IS NULL) = (lease_until IS NULL)),
  UNIQUE (kind, project_id, target_key)
);

CREATE INDEX storage_cleanup_jobs_due_idx
  ON storage_cleanup_jobs (available_at, lease_until, id);
