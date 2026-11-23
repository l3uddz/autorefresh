-- The episode numbers of the season that differs in a fix match decision:
-- those that only TMDB has, and those that only Plex has, each as a list of
-- numbers separated by commas. Older decisions keep the defaults, which mean
-- "not recorded".
ALTER TABLE event ADD COLUMN episode_missing TEXT NOT NULL DEFAULT '';
ALTER TABLE event ADD COLUMN episode_extra TEXT NOT NULL DEFAULT '';
