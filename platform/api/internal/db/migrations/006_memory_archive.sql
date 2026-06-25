-- Phase 2 pruning: soft-archive stale, unused memories so retrieval stays
-- crisp without losing history. The daily pruner sets archived_at; Search()
-- and List() exclude archived rows. Pinned memories are never archived.
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS archived_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS memory_archived_idx
    ON memory_entries (user_id, archived_at);
