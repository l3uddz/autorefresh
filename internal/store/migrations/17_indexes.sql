CREATE INDEX event_completed_item ON event (server, rating_key, at) WHERE kind = 'completed';

DROP INDEX run_server;
CREATE INDEX run_server_started ON run (server, started_at);

CREATE INDEX item_last_match_event ON item (last_match_event_id) WHERE last_match_event_id IS NOT NULL;

CREATE INDEX run_prune ON run (COALESCE(finished_at, started_at)) WHERE status != 'running';

DROP INDEX event_run;
CREATE INDEX event_run ON event (run_id, at) WHERE run_id IS NOT NULL;
