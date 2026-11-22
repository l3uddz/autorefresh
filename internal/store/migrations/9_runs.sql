-- One row for each server run or bulk job. A run has no library, because a
-- server can fail before it reads one, so the rows of a removed server are
-- deleted by code.
CREATE TABLE run (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    started_at INTEGER NOT NULL,
    finished_at INTEGER,
    read_items INTEGER NOT NULL DEFAULT 0,
    refreshed INTEGER NOT NULL DEFAULT 0,
    matched INTEGER NOT NULL DEFAULT 0,
    failed INTEGER NOT NULL DEFAULT 0,
    completed INTEGER NOT NULL DEFAULT 0,
    fixed INTEGER NOT NULL DEFAULT 0,
    incomplete INTEGER NOT NULL DEFAULT 0,
    due INTEGER NOT NULL DEFAULT 0,
    selected INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT ''
);

CREATE INDEX run_started ON run (started_at);
CREATE INDEX run_server ON run (server);
