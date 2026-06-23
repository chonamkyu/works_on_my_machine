-- Notification channels used by the platform.
--
-- This is informational only — channels are created on demand by NOTIFY.
-- The Go control plane runs the authoritative schema migrations from
-- api/internal/db/migrations on every startup.
--
-- Channels:
--   trigger:fired:<task_id>      trigger source fired
--   run:state:<run_id>           run state machine transition
--   run:log:<run_id>             one stdout/stderr line
--   evolution:proposed:<wf_id>   evolution engine drafted a PR
--   memory:written:<user_id>     reflection wrote a memory entry
--   connector:event:<user_id>    inbound connector event

CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_trgm;
