-- One row for each Plex write (refresh or match) that autorefresh sends.
-- A read skips an item while an attempt for it is live (ends_at at or after
-- the start of the read). outcome is pending until the answer is recorded;
-- a rejected attempt is deleted then. answered_seq is the newest attempt id
-- when the outcome was recorded. AUTOINCREMENT keeps an id from coming back.
CREATE TABLE write_attempt (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server TEXT NOT NULL,
    rating_key TEXT NOT NULL,
    action TEXT NOT NULL,
    started_at INTEGER NOT NULL,
    ends_at INTEGER NOT NULL,
    outcome TEXT NOT NULL DEFAULT 'pending',
    answered_seq INTEGER
);
CREATE INDEX write_attempt_key ON write_attempt (server, rating_key, ends_at);
CREATE INDEX write_attempt_ends ON write_attempt (ends_at);

-- A key that a read or a check could not apply. A library with a hold gets
-- a full read, and only a full read that reconciles the key deletes it.
CREATE TABLE read_hold (
    server TEXT NOT NULL,
    section TEXT NOT NULL,
    rating_key TEXT NOT NULL,
    PRIMARY KEY (server, section, rating_key),
    FOREIGN KEY (server, section) REFERENCES library (server, section) ON DELETE CASCADE
);
CREATE INDEX read_hold_key ON read_hold (server, rating_key);

-- The count of the prunings that removed rows a read can need: the row ''
-- for history and attempts, and one row for each server for the removal of
-- its libraries. A read is applied only if its generations did not change.
CREATE TABLE prune_fence (
    server TEXT PRIMARY KEY,
    generation INTEGER NOT NULL DEFAULT 0
);
INSERT INTO prune_fence (server) VALUES ('');
