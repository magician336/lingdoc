import { get, post } from '@/utils/request'
import type { Project, Result, TemplateField, TemplateSection, TemplateTerm } from './workspace'

export interface RuleSetRef { ruleset_id: string; version: string; ruleset_hash?: string }
export interface TemplateSourceRef { source_id: string; source_version: string; locator?: string }
export interface RuleDefinition {
  definition_id: string
  definition_version: string
  kind: string
  evaluator?: string
  allowed_target_kinds?: string[]
}
export interface RuleBinding {
  rule_id: string
  definition_id: string
  definition_version: string
  origin: string
  target_kind?: string
  target_id?: string
  parameters?: Record<string, unknown>
  severity: string
  message?: string
  trigger_scenes: string[]
  positive_example_refs?: string[]
  negative_example_refs?: string[]
  enabled: boolean
  owner: string
}
export interface RuleSet {
  ruleset_id: string
  version: string
  state: string
  bindings: RuleBinding[]
  ruleset_hash?: string
}
export interface TemplateDefinition {
  id?: string
  name?: string
  version?: string
  is_demo?: boolean
  source?: string
  scope?: string
  fields: TemplateField[]
  sections: TemplateSection[]
  terms?: TemplateTerm[]
  required_fields: string[]
  rules?: unknown[]
  content_hash?: string
  ruleset_hash?: string
}
export interface TemplateProfile {
  template_id: string
  name: string
  revision: number
  created_by: string
  created_at: string
}
export interface TemplateAuditEvent {
  id: number
  actor_user_id: string
  actor_role: string
  action: string
  target_type: string
  target_id: string
  outcome: string
  details: unknown
  created_at: string
}
export interface TemplateAuditHistory {
  events: TemplateAuditEvent[]
  next_after_id?: number
}
export interface BindingPolicy {
 rule_id: string
 may_disable: boolean
 allowed_target_kinds?: string[]
 allowed_target_ids?: string[]
 allowed_parameter_keys?: string[]
 parameter_bounds?: Record<string,{minimum?:number;maximum?:number}>
 allowed_severities?: string[]
}
export interface TemplateCatalogVersion {
 binding_policies: BindingPolicy[]
 policy_hash: string
  template_id: string
  version: string
  revision: number
  state: string
  definition: TemplateDefinition
  ruleset: RuleSetRef
  ruleset_snapshot: RuleSet
  source_type: string
  scope: string
  source_refs: TemplateSourceRef[]
  curator: string
  content_hash: string
  ruleset_hash: string
  published_at?: string
  recall?: { reason: string; migration_advice: string; affected_project_ids: string[]; recorded_at: string }
  created_by: string
  created_at: string
  updated_at: string
}
export interface SaveTemplateDraftInput {
 binding_policies?: BindingPolicy[]
  template_id: string
  version: string
  expected_revision: number
  definition: TemplateDefinition
  ruleset: RuleSetRef
  source_type: string
  scope: string
  source_refs: TemplateSourceRef[]
  curator: string
  reason: string
}
export interface TemplatePublishInput {
  template_id: string
  version: string
  expected_revision: number
  replay_fixture_ids: string[]
  reason: string
}
export interface TemplateGovernanceInput {
  template_id: string
  version: string
  expected_revision: number
  reason: string
}
export interface ProjectVersionBasis {
  project_version: number
  spec_revision: number
  context_revision: number
  template_copy_version: number
  template_id: string
  template_version: string
  ruleset: RuleSetRef
  current_snapshot_id?: string
}
export type GovernanceProject = Project & {
  ruleset_id?: string
  ruleset_version?: string
  ruleset_hash?: string
  current_snapshot_id?: string
  template_copy?: Project['template_copy'] & {
    ruleset_id?: string
    ruleset_version?: string
    rule_set_snapshot?: RuleSet
    ruleset_snapshot?: RuleSet
    rule_set_ref?: RuleSetRef
  }
}
export interface RuleSuggestionScope {
  project_id: string
  basis: ProjectVersionBasis
  template_id: string
  template_version: string
  catalog_revision: number
  ruleset: RuleSetRef
  target_kind: string
  target_id: string
  source_refs: TemplateSourceRef[]
}
export interface ProposedRule {
  rule_id: string
  definition_id: string
  definition_version: string
  target_kind: string
  target_id: string
  parameters?: Record<string, unknown>
  message: string
  rationale: string
  evidence_source_ids: string[]
}
export interface RuleSuggestionRun {
  id: string
  project_id: string
  actor_id: string
  revision: number
  status: string
  scope: RuleSuggestionScope
  output?: { proposals: ProposedRule[]; explanation: string }
  model_id?: string
  run_mode?: string
  prompt_version: string
  tool_names: string[]
  input_tokens: number
  output_tokens: number
  duration_ms: number
  error_code?: string
  error_message?: string
  decision_reason?: string
  draft_ruleset?: RuleSetRef
  created_at: string
  updated_at: string
}
export interface AcceptRuleSuggestionInput {
  expected_revision: number
  ruleset_id: string
  version: string
  bindings: RuleBinding[]
  reason: string
}
export interface RuleSetGovernanceInput {
  expected_state: string
  expected_ruleset_hash: string
  reason: string
  replay_fixture_ids?: string[]
  template_id?: string
  template_version?: string
}
export interface ReplayFixtureSummary {
  fixture_id: string
  rule_id: string
  scene: string
  polarity: string
  target_ref: string
  ruleset: RuleSetRef
  template_id: string
  template_version: string
}
const base = '/api/v1/lingdoc/template-catalog'
const segment = encodeURIComponent
const headers = (key: string) => ({ headers: { 'Idempotency-Key': key } })
const profilePath = (id: string) => `${base}/profiles/${segment(id)}`
const versionPath = (id: string, version: string) => `${profilePath(id)}/versions/${segment(version)}`
const rulePath = (id: string) => `${base}/rulesets/${segment(id)}/versions`
const suggestionPath = (id: string) => `/api/v1/lingdoc/projects/${segment(id)}/rule-suggestions`

export const listTemplateProfiles = (filter: { scope?: string; state?: string } = {}) =>
  get<Result<TemplateProfile[]>>(`${base}/profiles`, { params: filter })
export const createTemplateProfile = (input: { template_id: string; name: string; reason: string }, key: string) =>
  post<Result<TemplateProfile>>(`${base}/profiles`, input, headers(key))
export const listTemplateVersions = (id: string) => get<Result<TemplateCatalogVersion[]>>(`${profilePath(id)}/versions`)
export const listTemplateAuditHistory = (id: string, afterId?: number) =>
  get<Result<TemplateAuditHistory>>(`${profilePath(id)}/audit-events`, { params: afterId ? { after_id: afterId } : {} })
export const getTemplateVersion = (id: string, version: string) => get<Result<TemplateCatalogVersion>>(versionPath(id, version))
export const saveTemplateDraft = (input: SaveTemplateDraftInput, key: string) =>
  post<Result<TemplateCatalogVersion>>(`${profilePath(input.template_id)}/versions`, input, headers(key))
export const publishTemplateVersion = (input: TemplatePublishInput, key: string) =>
  post<Result<TemplateCatalogVersion>>(`${versionPath(input.template_id, input.version)}/publish`, input, headers(key))
export const withdrawTemplateVersion = (input: TemplateGovernanceInput, key: string) =>
  post<Result<TemplateCatalogVersion>>(`${versionPath(input.template_id, input.version)}/withdraw`, input, headers(key))
export const recallTemplateVersion = (input: TemplateGovernanceInput & { migration_advice: string }, key: string) =>
  post<Result<TemplateCatalogVersion>>(`${versionPath(input.template_id, input.version)}/recall`, input, headers(key))
export const listRuleDefinitions = () => get<Result<RuleDefinition[]>>(`${base}/rule-definitions`)
export const listReplayFixtures = () => get<Result<ReplayFixtureSummary[]>>(`${base}/replay-fixtures`)
export const listRuleSetVersions = (id: string) => get<Result<RuleSet[]>>(rulePath(id))
export const createRuleSetDraft = (draft: RuleSet, reason: string, key: string) =>
  post<Result<RuleSet>>(rulePath(draft.ruleset_id), { draft, reason }, headers(key))
export const governRuleSet = (id: string, version: string, action: 'publish' | 'withdraw' | 'recall', input: RuleSetGovernanceInput, key: string) =>
  post<Result<RuleSet>>(`${rulePath(id)}/${segment(version)}/${action}`, input, headers(key))
export const listRuleSuggestions = (projectId: string) => get<Result<RuleSuggestionRun[]>>(suggestionPath(projectId))
export const requestRuleSuggestion = (scope: RuleSuggestionScope, key: string) =>
  post<Result<RuleSuggestionRun>>(suggestionPath(scope.project_id), scope, headers(key))
export const getRuleSuggestion = (projectId: string, runId: string) =>
  get<Result<RuleSuggestionRun>>(`${suggestionPath(projectId)}/${segment(runId)}`)
export const acceptRuleSuggestion = (projectId: string, runId: string, input: AcceptRuleSuggestionInput, key: string) =>
  post<Result<RuleSuggestionRun>>(`${suggestionPath(projectId)}/${segment(runId)}/accept`, input, headers(key))
export const rejectRuleSuggestion = (projectId: string, runId: string, input: { expected_revision: number; reason: string }, key: string) =>
  post<Result<RuleSuggestionRun>>(`${suggestionPath(projectId)}/${segment(runId)}/reject`, input, headers(key))
