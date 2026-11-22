-- The title, type, agent, and language of a library, for the dashboard.
-- Older rows keep '' until the next read of the library fills them.
ALTER TABLE library ADD COLUMN title TEXT NOT NULL DEFAULT '';
ALTER TABLE library ADD COLUMN type TEXT NOT NULL DEFAULT '';
ALTER TABLE library ADD COLUMN agent TEXT NOT NULL DEFAULT '';
ALTER TABLE library ADD COLUMN language TEXT NOT NULL DEFAULT '';
