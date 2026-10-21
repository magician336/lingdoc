-- PF-2/3: the generation run learns which digest rule wrote its idempotency key.
--
-- The default is the current rule version: the rows that exist today were
-- hashed by the rule this build still speaks -- the legacy request payload is
-- byte identical, because the action fields are pointers with omitempty -- so
-- their keys keep replaying instead of turning into conflicts.
--
-- The column exists for the NEXT rule change: an old key then means "this key
-- belongs to an older rule", and the caller is told to retry with a new key
-- instead of being sent to look for a request that never changed.

ALTER TABLE lingdoc_generation_runs
    ADD COLUMN digest_version integer NOT NULL DEFAULT 1;
