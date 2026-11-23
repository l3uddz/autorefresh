-- The number of libraries that a run read fully and incrementally, so the
-- dashboard can tell a full run from an incremental one. Older runs keep 0
-- and 0.
ALTER TABLE run ADD COLUMN full_reads INTEGER NOT NULL DEFAULT 0;
ALTER TABLE run ADD COLUMN incremental_reads INTEGER NOT NULL DEFAULT 0;
