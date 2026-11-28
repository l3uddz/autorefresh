-- The rating key of the item that Plex kept when it merged a matched item
-- into another item with the same match. Only the completed event of such a
-- merge sets it.
ALTER TABLE event ADD COLUMN merged_key TEXT NOT NULL DEFAULT '';
