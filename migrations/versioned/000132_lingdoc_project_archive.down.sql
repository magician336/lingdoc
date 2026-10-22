-- 回退把归档这个终态从 status 的取值集合里拿掉。
--
-- 已归档的项目无法表示成旧的两个状态：丢弃是另一件事（discarded_at），归档
-- 没有等价的旧状态。这一步因此拒绝带着已归档的项目回退，而不是把它们悄悄
-- 改写成别的东西——那会让「项目已结束」这个事实消失。
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM lingdoc_projects WHERE status = 'archived') THEN
        RAISE EXCEPTION 'cannot roll back archived projects: the old status vocabulary has no archived state';
    END IF;
END;
$$;

ALTER TABLE lingdoc_projects
    DROP CONSTRAINT IF EXISTS lingdoc_projects_status_check;

ALTER TABLE lingdoc_projects
    ADD CONSTRAINT lingdoc_projects_status_check
    CHECK (status IN ('draft', 'active'));
