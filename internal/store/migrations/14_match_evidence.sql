-- The evidence of a fix match decision beyond its searches: the TMDB ID that
-- the year check compared, why the decision asked TMDB nothing, the folder
-- IDs that TMDB has no entry for, and the first season whose episode
-- numbers differ from TMDB, with the counts of Plex and TMDB. Older
-- decisions keep the defaults, which mean "not recorded".
ALTER TABLE event ADD COLUMN tmdb_id TEXT NOT NULL DEFAULT '';
ALTER TABLE event ADD COLUMN tmdb_skip TEXT NOT NULL DEFAULT '';
ALTER TABLE event ADD COLUMN tmdb_missing TEXT NOT NULL DEFAULT '';
ALTER TABLE event ADD COLUMN episode_season INTEGER NOT NULL DEFAULT 0;
ALTER TABLE event ADD COLUMN episode_plex INTEGER NOT NULL DEFAULT 0;
ALTER TABLE event ADD COLUMN episode_tmdb INTEGER NOT NULL DEFAULT 0;
