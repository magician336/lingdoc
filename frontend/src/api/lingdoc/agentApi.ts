import { get, post, put } from '@/utils/request'
import type { Project, Result, AssistantAffectedExplanation } from './workspace'

export interface VersionExpectation { expected_project_version: number; expected_context_revision: number; expected_version: number }
export interface ConfirmationInput extends VersionExpectation { confirmed: boolean; reason: string }
export interface VersionRef { id: string; version: number; kind?: string }
export interface VersionBasis {
  project_version: number; spec_revision: number; context_revision: number; chapter_id?: string; chapter_version_id?: string
  asset_versions: Array<{ asset_id: string; asset_revision: number }>; source_catalog_version: string
}
export interface ResearchTask {
  id: string; project_id: string; version: number; chapter_id?: string
  goal: string; question_type: string; scope: string; exclusions: string[]; queries: string[]; asset_ids: string[]; provider_id?: string; expected_output: string
  status: 'draft' | 'approved' | 'running' | 'completed' | 'failed' | 'cancelled' | 'stale'; paused: boolean; basis: VersionBasis; latest_run_id?: string; approved_by?: string
  created_by: string; created_at: string; updated_at: string
}
export interface SaveResearchTaskInput extends VersionExpectation {
  task_id?: string; chapter_id?: string; goal: string; question_type: string; scope: string
  exclusions: string[]; queries: string[]; asset_ids: string[]; provider_id?: string; expected_output: string
}
export type ResearchLevel = 'candidate_evidence' | 'background_lead' | 'discovery_entry' | 'unread_search_summary' | 'AI_GENERATED_CANDIDATE'
export interface ResearchResult {
  id: string; level: ResearchLevel; source_id?: string; asset_id?: string; asset_revision?: number; locator?: string; quoted_text_hash?: string
  title: string; url?: string; author?: string; published_at?: string; retrieved_at: string; provider: string; source_type: string; quality_note: string; uncertainty: string; summary?: string; status: string
}
export interface ResearchRun {
  id: string; project_id: string; version: number; task_id: string; task_version: number; basis: VersionBasis
  rounds: Array<{ number: number; query: string; provider: string; retrieved_at: string; result_ids: string[]; status: string; failure_code?: string; feedback?: string }>
  results: ResearchResult[]; status: string; failure_code?: string; run_mode: 'mock' | 'real_api_fake_model' | 'real'; correlation_id: string; created_by: string; started_at: string; finished_at?: string
}
export interface ImportCandidate {
  id: string; project_id: string; version: number; run_id?: string; result_id?: string; source_id?: string; knowledge_id?: string; knowledge_base_id?: string; url?: string; final_url?: string; title: string
  level: ResearchLevel; selection_type: string; conversation_session_id?: string; conversation_turn_ids?: string[]; locator?: string; quoted_text_hash?: string; purpose: string
  save_mode: 'full_text' | 'link_excerpt' | 'metadata_only'; scope: 'personal' | 'project' | 'team'; model_use_allowed: boolean; license: string; status: string; basis: VersionBasis
  asset_id?: string; asset_revision?: number; failure_code?: string; dedup_key: string; created_by: string; created_at: string
}
export interface CreateImportCandidateInput extends VersionExpectation {
  run_id?: string; result_id?: string; source_id?: string; url?: string; knowledge_base_id?: string; title: string; purpose: string; save_mode: ImportCandidate['save_mode']; scope: ImportCandidate['scope']
  model_use_allowed: boolean; license: string; conversation_session_id?: string; conversation_turn_ids?: string[]
}
export interface SourceAnchor {
  id: string; project_id: string; version: number; source_id: string; asset_id: string; asset_revision: number; locator: string; page?: number; section?: string; paragraph?: number; coordinates?: number[]
  quoted_text_hash: string; selection_start: number; selection_end: number; extraction_type: 'native_text' | 'ocr' | 'manual_correction'; ocr_confidence: number; human_verified: boolean
  original_ocr?: string; corrected_text?: string; correction_reason?: string; status: string; basis: VersionBasis; dedup_key: string; created_by: string; created_at: string
}
export interface SaveSourceAnchorInput extends VersionExpectation {
  anchor_id?: string; source_id: string; selection_start: number; selection_end: number; page?: number; section?: string; paragraph?: number; coordinates?: number[]
  extraction_type: SourceAnchor['extraction_type']; ocr_confidence: number; human_verified: boolean; corrected_text?: string; correction_reason?: string; confirmed: boolean
}
export interface ResearchClaim {
  id: string; project_id: string; version: number; text: string; scope: string; status: string; explanation?: string; basis: VersionBasis; reason: string; created_by: string; created_at: string
}
export interface SaveResearchClaimInput extends VersionExpectation {
  claim_id?: string; text: string; scope: string; status: 'active' | 'disputed' | 'superseded'; explanation?: string; reason: string; confirmed: boolean
}
export interface EvidenceCard {
  id: string; project_id: string; version: number; claim_ref?: VersionRef; anchor_refs: VersionRef[]; source_ids: string[]; support_type: string; scope: string; scope_chapter_ids: string[]
  limitations: string; gaps: string; alternative_explanation: string; status: 'CANDIDATE' | 'CONFIRMED' | 'NEEDS_REVIEW' | 'INSUFFICIENT' | 'STALE' | 'SUPERSEDED'; basis: VersionBasis; invalid_reason?: string; created_by: string; created_at: string
  confirmation?: { confirmed_by: string; confirmed_at: string; card_version: number; claim_ref: VersionRef; anchor_refs: VersionRef[]; context_revision: number; scope_chapter_ids: string[]; reason: string; meaning: string }
}
export interface SaveEvidenceCardInput extends VersionExpectation {
  card_id?: string; claim_ref?: VersionRef; anchor_refs: VersionRef[]; support_type: string; scope: string; scope_chapter_ids: string[]; limitations: string; gaps: string; alternative_explanation: string; confirmed: boolean
}
export interface AgentSourceRef { source_id: string; asset_id: string; asset_revision: number; quoted_text_hash: string; locator: string; quoted_text?: string; model_use_allowed: boolean }
export interface AgentContextPackage {
  project_id: string; project_version: number; spec_revision: number; context_revision: number; chapter_id?: string; chapter_version_id?: string; task_id?: string; task_version?: number
  template_id: string; template_version: string; source_refs: AgentSourceRef[]; card_refs: VersionRef[]; selection_reasons: string[]; authorization_state: string; output_constraints: string[]; correlation_id: string
}
export interface AgentContextInput extends VersionExpectation { chapter_id?: string; task_id?: string; source_ids: string[]; card_refs: VersionRef[]; sensitive_context_confirmed: boolean }
export type LingDocAgent = 'task_assistant' | 'research_agent'
export interface AgentSession { id: string; project_id: string; version: number; agent: LingDocAgent; created_by: string; created_at: string }
// 研究提案的形状由服务端契约拥有（internal/lingdoc/workspacecore/research_proposal.go）：
// 调研智能体的提案由服务端按那一份契约解码，解不开就拒绝，所以到达这里的形状已经是校验过的。
// 助手侧那张同名卡片另有自己的规则，尚未经这份契约校验（后续票收紧）。
// 字段名沿用调研任务自己的一套，保存时不必改名。
export interface ResearchProposal {
  goal: string; question_type?: string; scope: string
  exclusions?: string[]; queries: string[]; expected_output: string
}
export interface AgentTurn {
  id: string; project_id: string; session_id: string; version: number; agent: LingDocAgent; user_input: string; reply: string
  suggestions?: { reply?: string; task_card?: ResearchProposal; change_draft?: AgentChangeDraftSuggestion; affected_explanation?: AssistantAffectedExplanation }
  context: AgentContextPackage; status: string; failure_code?: string; model_id?: string; run_mode: string; input_tokens: number; output_tokens: number; created_by: string; created_at: string
}
// Only a real completed server turn can supply this provenance. No client grant flags.
export interface SaveAgentSpecCandidateInput {
  expected_spec_revision: number
  fields: Record<string, string>
  field_metadata: Record<string, {
    origin: 'ai_generated' | 'ai_assisted_human'
    status: 'pending_confirmation'
    provenance: { source_type: 'agent_turn'; source_id: string; source_version: string; purpose: 'project_spec'; locator: 'reply' }
  }>
}
export interface AgentChangeDraftSuggestion { expected_context_revision: number; reason: string; fields: Record<string,string>; affected_chapter_ids: string[] }
export interface ActionProposal {
  id: string; project_id: string; version: number; action: 'save_spec' | 'create_change_set' | 'bind_asset' | 'create_evidence_card' | 'confirm_evidence_card' | 'apply_change_set' | 'commit_working_copy' | 'export'
  target_id: string; input: Record<string, unknown>; input_hash: string; source_ids: string[]; basis: VersionBasis; expected_effect: string; reversibility: string; status: string; result?: unknown; failure_code?: string; created_by: string; created_at: string
}
export interface CreateActionProposalInput extends VersionExpectation { action: ActionProposal['action']; target_id: string; input: Record<string, unknown>; source_ids: string[]; expected_effect: string }
const segment = (id: string) => encodeURIComponent(id)
const project = (id: string) => '/api/v1/lingdoc/projects/' + segment(id)
const headers = (key: string) => ({ headers: { 'Idempotency-Key': key } })
const longRequest = (key: string) => ({ ...headers(key), timeout: 120000 })

export const getAgentSpecCandidateProject = (p: string) => get<Result<Project>>(project(p))
export const saveAgentSpecCandidate = (p: string, input: SaveAgentSpecCandidateInput, key: string) => put<Result<Project>>(project(p) + '/spec', input, headers(key))

export const listResearchTasks = (p: string) => get<Result<ResearchTask[]>>(project(p) + '/research-tasks')
export const saveResearchTask = (p: string, input: SaveResearchTaskInput, key: string) =>
  input.task_id ? put<Result<ResearchTask>>(project(p) + '/research-tasks/' + segment(input.task_id), input, headers(key))
    : post<Result<ResearchTask>>(project(p) + '/research-tasks', input, headers(key))
export const approveResearchTask = (p: string, id: string, input: ConfirmationInput, key: string) => post<Result<ResearchTask>>(project(p) + '/research-tasks/' + segment(id) + '/approve', input, headers(key))
export const runResearchTask = (p: string, id: string, input: VersionExpectation & { resume_run_id?: string }, key: string) => post<Result<ResearchRun>>(project(p) + '/research-tasks/' + segment(id) + '/run', input, longRequest(key))
export const controlResearchTask = (p: string, id: string, input: VersionExpectation & { action: 'pause' | 'resume' | 'cancel'; reason: string }, key: string) => post<Result<ResearchTask>>(project(p) + '/research-tasks/' + segment(id) + '/control', input, headers(key))
export const listResearchRuns = (p: string) => get<Result<ResearchRun[]>>(project(p) + '/research-runs')
export const getResearchRun = (p: string, id: string) => get<Result<ResearchRun>>(project(p) + '/research-runs/' + segment(id))
export const inspectResearchResult = (p: string, run: string, result: string) => post<Result<ResearchResult>>(project(p) + '/research-runs/' + segment(run) + '/results/' + segment(result) + '/inspect', {})
export const listImportCandidates = (p: string) => get<Result<ImportCandidate[]>>(project(p) + '/import-candidates')
export const createImportCandidate = (p: string, input: CreateImportCandidateInput, key: string) => post<Result<ImportCandidate>>(project(p) + '/import-candidates', input, headers(key))
export const confirmImportCandidate = (p: string, id: string, input: ConfirmationInput, key: string) => post<Result<ImportCandidate>>(project(p) + '/import-candidates/' + segment(id) + '/confirm', input, longRequest(key))
export const rejectImportCandidate = (p: string, id: string, input: ConfirmationInput, key: string) => post<Result<ImportCandidate>>(project(p) + '/import-candidates/' + segment(id) + '/reject', input, headers(key))
export const refreshImportCandidate = (p: string, id: string) => post<Result<ImportCandidate>>(project(p) + '/import-candidates/' + segment(id) + '/refresh', {})
export const listSourceAnchors = (p: string) => get<Result<SourceAnchor[]>>(project(p) + '/source-anchors')
export const saveSourceAnchor = (p: string, input: SaveSourceAnchorInput, key: string) => post<Result<SourceAnchor>>(project(p) + '/source-anchors', input, headers(key))
export const listResearchClaims = (p: string) => get<Result<ResearchClaim[]>>(project(p) + '/research-claims')
export const saveResearchClaim = (p: string, input: SaveResearchClaimInput, key: string) => post<Result<ResearchClaim>>(project(p) + '/research-claims', input, headers(key))
export const listEvidenceCards = (p: string) => get<Result<EvidenceCard[]>>(project(p) + '/evidence-cards')
export const saveEvidenceCard = (p: string, input: SaveEvidenceCardInput, key: string) => post<Result<EvidenceCard>>(project(p) + '/evidence-cards', input, headers(key))
export const confirmEvidenceCard = (p: string, id: string, input: ConfirmationInput, key: string) => post<Result<EvidenceCard>>(project(p) + '/evidence-cards/' + segment(id) + '/confirm', input, headers(key))
export const reviewEvidenceCard = (p: string, id: string, input: VersionExpectation & { status: 'INSUFFICIENT' | 'NEEDS_REVIEW' | 'SUPERSEDED'; confirmed: boolean; reason: string }, key: string) => post<Result<EvidenceCard>>(project(p) + '/evidence-cards/' + segment(id) + '/review', input, headers(key))
export const listAgentSessions = (p: string, agent: LingDocAgent) => get<Result<AgentSession[]>>(project(p) + '/agents/' + agent + '/sessions')
export const createAgentSession = (p: string, agent: LingDocAgent, input: VersionExpectation, key: string) => post<Result<AgentSession>>(project(p) + '/agents/' + agent + '/sessions', input, headers(key))
export const getAgentTurns = (p: string, id: string) => get<Result<AgentTurn[]>>(project(p) + '/agent-sessions/' + segment(id) + '/turns')
export const sendAgentTurn = (p: string, id: string, input: AgentContextInput & { user_input: string }, key: string) => post<Result<AgentTurn>>(project(p) + '/agent-sessions/' + segment(id) + '/turns', input, longRequest(key))
export const previewAgentContext = (p: string, input: AgentContextInput) => post<Result<AgentContextPackage>>(project(p) + '/agent-context', input)
export const listActionProposals = (p: string) => get<Result<ActionProposal[]>>(project(p) + '/action-proposals')
export const createActionProposal = (p: string, input: CreateActionProposalInput, key: string) => post<Result<ActionProposal>>(project(p) + '/action-proposals', input, headers(key))
export const confirmActionProposal = (p: string, id: string, input: ConfirmationInput, key: string) => post<Result<ActionProposal>>(project(p) + '/action-proposals/' + segment(id) + '/confirm', input, longRequest(key))
export const rejectActionProposal = (p: string, id: string, input: ConfirmationInput, key: string) => post<Result<ActionProposal>>(project(p) + '/action-proposals/' + segment(id) + '/reject', input, headers(key))

export interface ProjectAssetPolicy {
 id: string; project_id: string; asset_id: string; version: number; asset_revision: number; policy_version: number
 scope: 'personal' | 'project' | 'team'; purpose: string; allowed_user_ids: string[]; model_use_allowed: boolean; reason: string
 created_by: string; granted_by: string; source_version_hash: string; context_revision: number; updated_at: string
}
export interface ProjectAssetPolicyProposalInput extends ConfirmationInput {
 policy: Pick<ProjectAssetPolicy, 'scope' | 'purpose' | 'allowed_user_ids' | 'model_use_allowed'>
}
export const getProjectAssetPolicy = (p: string, id: string) => get<Result<ProjectAssetPolicy>>(project(p) + '/assets/' + segment(id) + '/policy')
export const proposeProjectAssetPolicy = (p: string, id: string, input: ProjectAssetPolicyProposalInput, key: string) => post<Result<unknown>>(project(p) + '/assets/' + segment(id) + '/policy/proposals', input, headers(key))
