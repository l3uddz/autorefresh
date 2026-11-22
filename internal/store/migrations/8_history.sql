-- One row for each thing that happened to an item. The row keeps a snapshot
-- of the item, so it still renders after the item row is gone. The events of
-- a library go with the library. A completion event points at the match
-- event that fixed the item, and the pointer clears when that event goes.
-- AUTOINCREMENT keeps a deleted ID from coming back.
CREATE TABLE event (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server TEXT NOT NULL DEFAULT '',
    section TEXT NOT NULL DEFAULT '',
    rating_key TEXT NOT NULL DEFAULT '',
    at INTEGER NOT NULL,
    kind TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    year INTEGER NOT NULL DEFAULT 0,
    type TEXT NOT NULL DEFAULT '',
    thumb TEXT NOT NULL DEFAULT '',
    art TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL DEFAULT '',
    accepted INTEGER NOT NULL DEFAULT 0,
    outcome TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    match_guid TEXT NOT NULL DEFAULT '',
    match_title TEXT NOT NULL DEFAULT '',
    match_year INTEGER NOT NULL DEFAULT 0,
    conflict TEXT NOT NULL DEFAULT '',
    path_year INTEGER NOT NULL DEFAULT 0,
    tmdb_year INTEGER NOT NULL DEFAULT 0,
    episode_count INTEGER NOT NULL DEFAULT 0,
    links TEXT NOT NULL DEFAULT '',
    fixed_action TEXT NOT NULL DEFAULT '',
    fixed_match TEXT NOT NULL DEFAULT '',
    fixed_match_event_id INTEGER REFERENCES event (id) ON DELETE SET NULL,
    FOREIGN KEY (server, section) REFERENCES library (server, section) ON DELETE CASCADE
);

CREATE INDEX event_item ON event (server, rating_key, at);
CREATE INDEX event_kind ON event (kind, at);
CREATE INDEX event_at ON event (at);
CREATE INDEX event_library ON event (server, section);
CREATE INDEX event_fixed_match ON event (fixed_match_event_id);

-- One row for each Plex search of a match decision event.
CREATE TABLE match_search (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id INTEGER NOT NULL REFERENCES event (id) ON DELETE CASCADE,
    position INTEGER NOT NULL DEFAULT 0,
    query TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT '',
    from_id TEXT NOT NULL DEFAULT '',
    failed INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX match_search_event ON match_search (event_id);

-- One row for each candidate that a search returned.
CREATE TABLE match_candidate (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    search_id INTEGER NOT NULL REFERENCES match_search (id) ON DELETE CASCADE,
    position INTEGER NOT NULL DEFAULT 0,
    guid TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    year INTEGER NOT NULL DEFAULT 0,
    type TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    thumb TEXT NOT NULL DEFAULT '',
    score INTEGER NOT NULL DEFAULT 0,
    verdict TEXT NOT NULL DEFAULT ''
);

CREATE INDEX match_candidate_search ON match_candidate (search_id);
