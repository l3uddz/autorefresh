-- Whether a run links the events that it writes. Runs from before 12_event_run
-- linked none, so the dashboard says that their items were not recorded
-- instead of saying that they changed none. New runs get 1.
ALTER TABLE run ADD COLUMN events_linked INTEGER NOT NULL DEFAULT 1;
UPDATE run SET events_linked = 0;
