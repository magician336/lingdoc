-- Keep the exact immutable RuleSet reference directly on each export record.
ALTER TABLE lingdoc_export_artifacts
    ADD COLUMN ruleset_ref_json text NOT NULL DEFAULT '';
