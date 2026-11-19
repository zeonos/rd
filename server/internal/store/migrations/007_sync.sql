-- Syncthing status the agent reports every few minutes (JSON, '' = never).
ALTER TABLE devices ADD COLUMN sync    TEXT NOT NULL DEFAULT '';
ALTER TABLE devices ADD COLUMN sync_at TIMESTAMPTZ;
