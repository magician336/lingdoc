BEGIN;

DROP INDEX IF EXISTS idx_knowledges_summary_status;
ALTER TABLE knowledges DROP COLUMN IF EXISTS summary_status;

ALTER TABLE knowledge_bases 
DROP COLUMN IF EXISTS question_generation_config;

ALTER TABLE users
    DROP COLUMN IF EXISTS can_access_all_tenants;

ALTER TABLE knowledge_bases
    ADD COLUMN IF NOT EXISTS rerank_model_id VARCHAR(64);

-- Undo the models.tenant_id back-fill that 000001_agent.up.sql performs.
--
-- The up side builds its list of "models a knowledge base points at" from
-- knowledge_bases.embedding_model_id and knowledge_bases.summary_model_id (its
-- tenant_source CTE) and raises models.tenant_id from 0 to the knowledge
-- base's tenant.  This statement is the exact mirror of that: it rebuilds the
-- same list from the same two columns and resets models.tenant_id back to 0
-- for the rows the up side raised.
--
-- The previous spelling joined models to knowledges on "m.id = k.id".
-- models.id and knowledges.id are two independently generated random UUIDv4
-- primary keys with no foreign key between them, so that join matched almost
-- nothing: the back-fill was never actually undone.  When the two id spaces
-- happened to collide it touched the wrong row instead.
--
-- Same-source invariant: the list below must stay identical to the up side's
-- tenant_source.  internal/database/migration_agent_tenant_source_test.go
-- fails when the two drift apart.
WITH tenant_source AS (
    SELECT kb.embedding_model_id AS model_id, kb.tenant_id
    FROM knowledge_bases kb
    WHERE kb.tenant_id IS NOT NULL AND kb.embedding_model_id IS NOT NULL AND kb.embedding_model_id <> ''
    UNION
    SELECT kb.summary_model_id AS model_id, kb.tenant_id
    FROM knowledge_bases kb
    WHERE kb.tenant_id IS NOT NULL AND kb.summary_model_id IS NOT NULL AND kb.summary_model_id <> ''
)
UPDATE models m
SET tenant_id = 0
FROM tenant_source ts
WHERE m.id = ts.model_id AND m.tenant_id = ts.tenant_id;

-- Drop index on content_hash
DROP INDEX IF EXISTS idx_chunks_content_hash;

-- Drop content_hash column
ALTER TABLE chunks
    DROP COLUMN IF EXISTS content_hash;

-- Drop status column
ALTER TABLE chunks
    DROP COLUMN IF EXISTS status;

-- Drop indexes and columns referencing tags
DROP INDEX IF EXISTS idx_chunks_tag;
ALTER TABLE chunks DROP COLUMN IF EXISTS tag_id;

DROP INDEX IF EXISTS idx_knowledges_tag;
ALTER TABLE knowledges DROP COLUMN IF EXISTS tag_id;

-- Drop tag table
DROP INDEX IF EXISTS idx_knowledge_tags_kb_name;
DROP INDEX IF EXISTS idx_knowledge_tags_kb;
DROP TABLE IF EXISTS knowledge_tags;

ALTER TABLE chunks
  DROP COLUMN IF EXISTS metadata;

ALTER TABLE knowledge_bases
  DROP COLUMN IF EXISTS faq_config,
  DROP COLUMN IF EXISTS type;

-- Drop index
DROP INDEX IF EXISTS idx_models_is_builtin;

-- Remove is_builtin column
ALTER TABLE models 
DROP COLUMN IF EXISTS is_builtin;

-- Remove check constraint
ALTER TABLE mcp_services
DROP CONSTRAINT IF EXISTS chk_mcp_transport_config;

-- Make url column required again
ALTER TABLE mcp_services 
ALTER COLUMN url SET NOT NULL;

-- Remove stdio_config and env_vars columns
ALTER TABLE mcp_services 
DROP COLUMN IF EXISTS env_vars,
DROP COLUMN IF EXISTS stdio_config;

-- Remove web_search_config column
ALTER TABLE tenants 
DROP COLUMN IF EXISTS web_search_config;

-- Drop trigger
DROP TRIGGER IF EXISTS trigger_mcp_services_updated_at ON mcp_services;

-- Drop function
DROP FUNCTION IF EXISTS update_mcp_services_updated_at();

-- Drop indexes
DROP INDEX IF EXISTS idx_mcp_services_tenant_id;
DROP INDEX IF EXISTS idx_mcp_services_enabled;
DROP INDEX IF EXISTS idx_mcp_services_deleted_at;

-- Drop table
DROP TABLE IF EXISTS mcp_services;

-- This migration performs a data cleanup (soft delete) which is not safely reversible.
-- Down migration is a no-op.

-- Drop foreign key constraints first
ALTER TABLE auth_tokens DROP CONSTRAINT IF EXISTS fk_auth_tokens_user;
ALTER TABLE users DROP CONSTRAINT IF EXISTS fk_users_tenant;

-- Drop tables
DROP TABLE IF EXISTS auth_tokens;
DROP TABLE IF EXISTS users;

-- Drop is_temporary column from knowledge_bases
ALTER TABLE knowledge_bases
    DROP COLUMN IF EXISTS is_temporary;

-- Drop context_config column from tenants
ALTER TABLE tenants
    DROP COLUMN IF EXISTS context_config;

-- Drop conversation_config column from tenants
ALTER TABLE tenants
    DROP COLUMN IF EXISTS conversation_config;

-- Drop JSONB indexes if they exist
DROP INDEX IF EXISTS idx_messages_agent_steps;
DROP INDEX IF EXISTS idx_sessions_context_config;
DROP INDEX IF EXISTS idx_sessions_agent_config;
DROP INDEX IF EXISTS idx_tenants_agent_config;

-- Drop columns if they exist
ALTER TABLE messages
    DROP COLUMN IF EXISTS agent_steps;

ALTER TABLE sessions
    DROP COLUMN IF EXISTS context_config;

ALTER TABLE sessions
    DROP COLUMN IF EXISTS agent_config;

ALTER TABLE tenants
    DROP COLUMN IF EXISTS agent_config;

COMMIT;
