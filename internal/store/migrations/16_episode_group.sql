-- The name of the alternate TMDB episode group that confirmed a series match
-- when TMDB's default seasons did not. Empty means default seasons or no
-- episode confirmation.
ALTER TABLE event ADD COLUMN episode_group TEXT NOT NULL DEFAULT '';
