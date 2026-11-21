-- The last request of an item that Plex accepted, and the last accepted
-- match request: 'refresh', 'match', or 'tmdb-match'. A later refresh does
-- not replace last_match, so a completed item still shows a fix match.
-- Older rows keep '' because their requests were not recorded.
ALTER TABLE item ADD COLUMN last_action TEXT NOT NULL DEFAULT '';
ALTER TABLE item ADD COLUMN last_match TEXT NOT NULL DEFAULT '';
