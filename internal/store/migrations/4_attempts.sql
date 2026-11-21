-- The number of attempts of an item: refresh and match requests, successful
-- or failed, and failed match searches. The queue puts the items with the
-- fewest attempts first, so an item that keeps failing moves back. The wait
-- still uses refreshes, so a failed attempt does not make it longer.
ALTER TABLE item ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;

-- Older rows count only their refreshes, because failed attempts were not
-- counted before.
UPDATE item SET attempts = refreshes;
