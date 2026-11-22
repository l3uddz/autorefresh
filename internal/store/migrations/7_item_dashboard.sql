-- What the dashboard shows of an item. stopped_at is null while the item is
-- not stopped. last_match_event_id is the last match decision event of the
-- item. It has no foreign key, because the event table is created later and
-- SQLite cannot add a foreign key to a table. Older rows keep '' and null.
ALTER TABLE item ADD COLUMN stopped_at INTEGER;
ALTER TABLE item ADD COLUMN thumb TEXT NOT NULL DEFAULT '';
ALTER TABLE item ADD COLUMN art TEXT NOT NULL DEFAULT '';
ALTER TABLE item ADD COLUMN path TEXT NOT NULL DEFAULT '';
ALTER TABLE item ADD COLUMN last_outcome TEXT NOT NULL DEFAULT '';
ALTER TABLE item ADD COLUMN last_match_outcome TEXT NOT NULL DEFAULT '';
ALTER TABLE item ADD COLUMN last_match_event_id INTEGER;
