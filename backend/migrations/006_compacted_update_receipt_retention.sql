CREATE INDEX board_updates_compacted_receipts_expiry_idx
  ON board_updates (compacted_at, board_id, server_sequence)
  WHERE compacted_at IS NOT NULL;
