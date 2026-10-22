-- PF-2/9 rollback: the language review report table goes away.
--
-- The reports that exist today go with it: a report is a proposal that was
-- never applied to a chapter, so dropping it does not touch any content.

DROP TABLE IF EXISTS lingdoc_review_reports;
