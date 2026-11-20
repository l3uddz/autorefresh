-- Each item row needs the library row of its section. An item without one
-- gets a library row with no read times, so the item keeps its history and
-- the next run does a full read of the library.
INSERT INTO library (server, section)
SELECT DISTINCT server, section FROM item
WHERE NOT EXISTS (
    SELECT 1 FROM library WHERE library.server = item.server AND library.section = item.section
);

-- SQLite cannot add a foreign key to a table, so the table is rebuilt.
CREATE TABLE item_new (
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
    PRIMARY KEY (server, rating_key),
    FOREIGN KEY (server, section) REFERENCES library (server, section) ON DELETE CASCADE
);

INSERT INTO item_new (server, section, rating_key, type, title, year, added_at, reasons,
    last_refresh, last_attempt, refreshes)
SELECT server, section, rating_key, type, title, year, added_at, reasons,
    last_refresh, last_attempt, refreshes
FROM item;

DROP TABLE item;
ALTER TABLE item_new RENAME TO item;

CREATE INDEX item_library ON item (server, section);
