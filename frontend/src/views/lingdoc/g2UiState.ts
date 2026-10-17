import type { Project } from '@/api/lingdoc/workspace'
import type { AgentSession, AgentTurn, SaveAgentSpecCandidateInput } from '@/api/lingdoc/g2'

export function nonemptyResearchQueries(value: string): string[] {
  return value.split(/\r?\n/).map(line => line.trim()).filter(Boolean)
}
export function selectedRuneRange(full: string, before: string, selected: string): { start: number; end: number; text: string } | null {
  if (!selected || !full.startsWith(before) || full.slice(before.length, before.length + selected.length) !== selected) return null
  const start = Array.from(before).length
  return { start, end: start + Array.from(selected).length, text: selected }
}
export function safeResearchUrl(value?: string): string | null {
  try {
    const url = new URL(value || '')
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password) return null
    return url.href
  } catch { return null }
}
export class G2OperationKeys {
  private entries = new Map<string, { payload: string; key: string }>()
  constructor(private createKey: () => string = () => 'lingdoc-g2-' + crypto.randomUUID()) {}
  forRequest(slot: string, payload: unknown): string {
    const stamp = JSON.stringify(payload)
    const previous = this.entries.get(slot)
    if (previous?.payload === stamp) return previous.key
    const entry = { payload: stamp, key: this.createKey() }
    this.entries.set(slot, entry)
    return entry.key
  }
  accepted(slot: string) { this.entries.delete(slot) }
  clear() { this.entries.clear() }
}
export class G2ResponseEpoch {
  private epoch = 0
  next() { return ++this.epoch }
  capture() { return this.epoch }
  accepts(epoch: number) { return this.epoch === epoch }
}

/** UI basis only. The server remains the authority for turn identity and source permission. */
export interface AgentSpecCandidateBasis {
  projectID: string; userID: string; tenantID: string; projectVersion: number; specRevision: number; contextRevision: number
  templateCopyID: string; templateCopyVersion: number; templateID: string; templateVersion: string
  templateStamp: string; fieldsStamp: string
}
export function pinAgentSpecCandidateBasis(p: Project, userID: string, tenantID: string): AgentSpecCandidateBasis {
  return {
    projectID: p.id, userID, tenantID, projectVersion: p.project_version, specRevision: p.spec_revision, contextRevision: p.current_context_revision,
    templateCopyID: p.template_copy?.id || '', templateCopyVersion: p.template_copy_version ?? p.template_copy?.version ?? 0,
    templateID: p.template_id, templateVersion: p.template_version,
    templateStamp: JSON.stringify([p.template_copy?.content_hash || '', p.template_copy?.definition.fields || []]),
    fieldsStamp: JSON.stringify(Object.entries(p.spec).sort(([a], [b]) => a.localeCompare(b))),
  }
}
export function agentSpecCandidateBasisCurrent(basis: AgentSpecCandidateBasis, p: Project, userID: string, tenantID: string): boolean {
  return JSON.stringify(basis) === JSON.stringify(pinAgentSpecCandidateBasis(p, userID, tenantID)) && p.status === 'draft'
}
export function eligibleAgentSpecTurn(turn: AgentTurn, session: AgentSession | undefined, p: Project, userID: string): boolean {
  return !!userID && p.status === 'draft' && turn.status === 'completed' && !!turn.reply?.trim()
    && !!turn.id && Number.isSafeInteger(turn.version) && turn.version > 0 && turn.project_id === p.id && turn.created_by === userID
    && !!session && Number.isSafeInteger(session.version) && session.version > 0 && session.id === turn.session_id && session.project_id === p.id && session.created_by === userID
}
export function buildAgentSpecCandidateInput(p: Project, turn: AgentTurn, field: string, value: string): SaveAgentSpecCandidateInput {
  if (p.status !== 'draft' || !p.template_copy?.definition.fields.some(item => item.field_id === field) || !value.trim()
    || turn.project_id !== p.id || !turn.id || turn.status !== 'completed' || !turn.reply?.trim() || !Number.isSafeInteger(turn.version) || turn.version < 1) {
    throw new Error('请从当前草稿模板选择字段，并使用已完成且可读取的真实会话。')
  }
  const edited = value.trim()
  if (new TextEncoder().encode(edited).length > 10000) throw new Error('候选值超过研究条件字段的 UTF-8 字节限制，请先人工整理。')
  return { expected_spec_revision: p.spec_revision, fields: { ...p.spec, [field]: edited }, field_metadata: {
    [field]: { origin: edited === turn.reply.trim() ? 'ai_generated' : 'ai_assisted_human', status: 'pending_confirmation',
      provenance: { source_type: 'agent_turn', source_id: turn.id, source_version: String(turn.version), purpose: 'project_spec', locator: 'reply' } },
  } }
}
export function acceptedAgentSpecCandidate(p: Project | undefined, projectID: string, input: SaveAgentSpecCandidateInput): boolean {
  const field = Object.keys(input.field_metadata)[0]
  if (!field || !p || p.id !== projectID || p.status !== 'draft' || !Number.isSafeInteger(p.spec_revision) || p.spec_revision < input.expected_spec_revision) return false
  if (!Object.entries(input.fields).every(([key, value]) => p.spec?.[key] === value)) return false
  const meta = p.spec_fields?.[field], expected = input.field_metadata[field]!
  return !!meta && meta.value === input.fields[field] && meta.status === 'pending_confirmation'
    && ['ai_generated', 'ai_assisted_human'].includes(meta.origin) && meta.provenance?.source_type === 'agent_turn'
    && meta.provenance.source_id === expected.provenance.source_id && meta.provenance.source_version === expected.provenance.source_version
}

export function agentSpecRunModeLabel(mode: string): string {
  return ({ real: '真实模型会话', mock: '模拟回复，未调用真实模型', real_api_fake_model: '真实接口的模拟模型回复，非真实模型结果' } as Record<string, string>)[mode] || '运行方式未确认'
}
