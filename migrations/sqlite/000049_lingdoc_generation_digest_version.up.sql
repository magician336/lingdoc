-- PF-2/3: the generation run learns which digest rule wrote its idempotency key.
--
-- SQLite adds a column with its default in place: the rows that exist today
-- keep the digest they have, and they gain the current rule version, so their
-- keys keep replaying instead of turning into conflicts.

ALTER TABLE lingdoc_generation_runs ADD COLUMN digest_version INTEGER NOT NULL DEFAULT 1;
