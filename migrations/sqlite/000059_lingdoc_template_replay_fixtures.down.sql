-- 回退 #769：删除回放夹具表。表是 append-only 的登记证据，删表即删除这一
-- 段发布证据链；旧行随表消失后，历史发布仍可按当时的项目副本与审计回执复核。

DROP TABLE IF EXISTS lingdoc_template_replay_fixtures;
