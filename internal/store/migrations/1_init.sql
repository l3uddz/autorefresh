CREATE TABLE library (
    server TEXT NOT NULL,
    section TEXT NOT NULL,
    last_full_read INTEGER,
    last_read INTEGER,
    PRIMARY KEY (server, section)
);

CREATE TABLE item (
    server TEXT NOT NULL,
    section TEXT NOT NULL,
    rating_key TEXT NOT NULL,
    type TEXT NOT NULL,
    title TEXT NOT NULL,
    year INTEGER NOT NULL,
    added_at INTEGER NOT NULL,
    reasons INTEGER NOT NULL,
    last_refresh INTEGER,
    last_attempt INTEGER,
    refreshes INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (server, rating_key)
);
