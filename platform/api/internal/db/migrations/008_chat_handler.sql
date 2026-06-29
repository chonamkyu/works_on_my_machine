-- Chat-handler tasks: when the workflow code declares
-- workflow.chatHandler(...), the platform routes user chat messages
-- straight to the running daemon instead of through the meta-generator.
-- The flag is set at task create/update time by parsing main.ts.
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS has_chat_handler BOOLEAN NOT NULL DEFAULT false;
