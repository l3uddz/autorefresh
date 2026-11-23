-- The run that wrote an event, so the dashboard can list the items of a
-- run. Manual actions outside a run and older events keep null. A run that
-- retention or a server prune removes leaves its events, without the link.
ALTER TABLE event ADD COLUMN run_id INTEGER REFERENCES run (id) ON DELETE SET NULL;
CREATE INDEX event_run ON event (run_id);
