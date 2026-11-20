-- The TMDB ID that TMDB gave for an external path ID of an item. The rows
-- go with their item.
CREATE TABLE tmdb_id (
    server TEXT NOT NULL,
    rating_key TEXT NOT NULL,
    source TEXT NOT NULL,
    external_id TEXT NOT NULL,
    tmdb_id TEXT NOT NULL,
    PRIMARY KEY (server, rating_key, source, external_id),
    FOREIGN KEY (server, rating_key) REFERENCES item (server, rating_key) ON DELETE CASCADE
);
