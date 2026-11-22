-- Count items skipped after selection.
ALTER TABLE run ADD COLUMN skipped INTEGER NOT NULL DEFAULT 0;

-- Keep bulk results in selection order and remove them with the run.
CREATE TABLE run_item (
    run_id INTEGER NOT NULL REFERENCES run (id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    rating_key TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    outcome TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (run_id, position)
);
