CREATE TABLE IF NOT EXISTS schema_migrations (
  version BIGINT PRIMARY KEY,
  name TEXT NOT NULL,
  checksum TEXT NOT NULL,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE users
  ADD COLUMN IF NOT EXISTS must_change_password BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN IF NOT EXISTS password_changed_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS sessions (
  token_hash TEXT PRIMARY KEY CHECK (char_length(token_hash) = 64),
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TIMESTAMPTZ NOT NULL,
  last_accessed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (expires_at > created_at)
);

CREATE INDEX IF NOT EXISTS sessions_user_id_idx ON sessions(user_id);
CREATE INDEX IF NOT EXISTS sessions_expires_at_idx ON sessions(expires_at);

DROP TABLE IF EXISTS board_operations;
DROP TABLE IF EXISTS board_snapshots;
ALTER TABLE boards DROP COLUMN IF EXISTS version;

CREATE TABLE IF NOT EXISTS board_documents (
  board_id TEXT PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
  checkpoint BYTEA NOT NULL DEFAULT ''::bytea,
  checkpoint_sequence BIGINT NOT NULL DEFAULT 0 CHECK (checkpoint_sequence >= 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO board_documents (board_id, checkpoint, checkpoint_sequence, updated_at)
SELECT id, ''::bytea, 0, updated_at
FROM boards
ON CONFLICT (board_id) DO NOTHING;

CREATE TABLE IF NOT EXISTS board_updates (
  board_id TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
  server_sequence BIGINT NOT NULL CHECK (server_sequence > 0),
  update_id TEXT NOT NULL CHECK (update_id <> ''),
  client_id TEXT NOT NULL CHECK (client_id <> ''),
  user_id TEXT NOT NULL REFERENCES users(id),
  update_data BYTEA CHECK (update_data IS NULL OR octet_length(update_data) > 0),
  update_hash TEXT NOT NULL CHECK (update_hash ~ '^[0-9a-f]{64}$'),
  compacted_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (
    (update_data IS NOT NULL AND compacted_at IS NULL)
    OR (update_data IS NULL AND compacted_at IS NOT NULL)
  ),
  PRIMARY KEY (board_id, server_sequence),
  UNIQUE (board_id, update_id)
);

CREATE INDEX IF NOT EXISTS board_updates_board_created_at_idx
  ON board_updates(board_id, created_at);

ALTER TABLE assets
  ADD COLUMN IF NOT EXISTS storage_key TEXT,
  ADD COLUMN IF NOT EXISTS sha256 TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS width INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS height INTEGER NOT NULL DEFAULT 0;

UPDATE assets
SET storage_key = id
WHERE storage_key IS NULL OR storage_key = '';

ALTER TABLE assets ALTER COLUMN storage_key SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS assets_storage_key_idx ON assets(storage_key);
CREATE INDEX IF NOT EXISTS assets_project_id_idx ON assets(project_id);

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conname = 'assets_nonnegative_metadata_check'
      AND conrelid = 'assets'::regclass
  ) THEN
    ALTER TABLE assets
      ADD CONSTRAINT assets_nonnegative_metadata_check
      CHECK (size >= 0 AND width >= 0 AND height >= 0);
  END IF;
END;
$$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conname = 'assets_sha256_format_check'
      AND conrelid = 'assets'::regclass
  ) THEN
    ALTER TABLE assets
      ADD CONSTRAINT assets_sha256_format_check
      CHECK (sha256 = '' OR sha256 ~ '^[0-9a-f]{64}$');
  END IF;
END;
$$;
