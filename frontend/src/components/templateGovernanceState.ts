import type {
  GovernanceProject, ProjectVersionBasis, RegisterReplayFixturesResult, ReplayCoverageMatrix, ReplayCoverageView, ReplayFixtureInput, ReplayFixtureSummary, RulesetProjectionRef, RuleBinding, RuleSetRef, RuleSuggestionRun,
  SaveTemplateDraftInput, TemplateCatalogVersion, TemplateSourceRef,
} from '@/api/lingdoc/templateGovernance'

const stateLabels: Record<string, string> = {
  draft: '待审核草稿', published: '已发布', withdrawn: '已撤回', recalled: '已召回',
  running: '模型处理中', suggestion: '建议待人工决定', stale: '输入已变化，需重新请求',
  failed: '请求失败', accepted: '已保存为规则草稿', rejected: '已拒绝',
}
export const stateLabel = (state: string) => stateLabels[state] || `待识别状态（${state || '未提供'}）`
const modeLabels: Record<string, string> = {
  real: '真实模型', mock: '模拟模型', real_api_fake_model: '真实接口 / 模拟模型',
}
export const runModeLabel = (mode?: string) => modeLabels[mode || ''] || `运行方式未确认${mode ? `（${mode}）` : ''}`

export function projectRuleSet(project: GovernanceProject): RuleSetRef {
  const copy = project.template_copy
  const snapshot = copy?.rule_set_snapshot || copy?.ruleset_snapshot
  if (snapshot) return { ruleset_id: snapshot.ruleset_id, version: snapshot.version, ruleset_hash: snapshot.ruleset_hash || '' }
  if (copy?.rule_set_ref) return { ...copy.rule_set_ref }
  return {
    ruleset_id: copy?.ruleset_id || project.ruleset_id || '',
    version: copy?.ruleset_version || project.ruleset_version || '',
    ruleset_hash: copy?.ruleset_hash || project.ruleset_hash || '',
  }
}
export function projectBasis(project: GovernanceProject): ProjectVersionBasis {
  return {
    project_version: project.project_version,
    spec_revision: project.spec_revision,
    context_revision: project.current_context_revision,
    template_copy_version: project.template_copy_version || 0,
    template_id: project.template_id,
    template_version: project.template_version,
    ruleset: projectRuleSet(project),
    current_snapshot_id: project.current_snapshot_id || '',
  }
}
export function parseSourceRefs(value: string): TemplateSourceRef[] {
  if (!value.trim()) return []
  return value.split('\n').map(line => line.trim()).filter(Boolean).map(line => {
    const at = line.lastIndexOf('@')
    if (at <= 0 || at === line.length - 1) throw new Error('来源每行填写真实 source_id@source_version。')
    return { source_id: line.slice(0, at).trim(), source_version: line.slice(at + 1).trim() }
  })
}
export function splitIds(value: string): string[] {
  return [...new Set(value.split(/[\s,，]+/).map(item => item.trim()).filter(Boolean))]
}
export function cloneDraftInput(version: TemplateCatalogVersion): SaveTemplateDraftInput {
  return {
    template_id: version.template_id, version: version.version, expected_revision: version.revision,
    definition: {
      name: version.definition.name,
      fields: cloneJSON(version.definition.fields || []),
      sections: cloneJSON(version.definition.sections || []),
      terms: cloneJSON(version.definition.terms || []),
      required_fields: [...(version.definition.required_fields || [])],
    },
    binding_policies: cloneJSON(version.binding_policies || []), ruleset: { ...version.ruleset }, source_type: version.source_type, scope: version.scope,
    source_refs: cloneJSON(version.source_refs || []), curator: version.curator, reason: '',
  }
}
export function suggestionBindings(run: RuleSuggestionRun): RuleBinding[] {
  return (run.output?.proposals || []).map(proposal => ({
    rule_id: proposal.rule_id, definition_id: proposal.definition_id, definition_version: proposal.definition_version,
    origin: 'project', target_kind: proposal.target_kind, target_id: proposal.target_id,
    parameters: cloneJSON(proposal.parameters || {}), severity: 'info', message: proposal.message,
    trigger_scenes: ['write_check', 'preflight'], positive_example_refs: [], negative_example_refs: [],
    enabled: true, owner: run.actor_id,
  }))
}
export interface MutationAttempt { signature: string; key: string }
export function mutationAttempt(previous: MutationAttempt | undefined, input: unknown, mint: () => string): MutationAttempt {
  const signature = JSON.stringify(input)
  return previous?.signature === signature ? previous : { signature, key: mint() }
}


function cloneJSON<T>(value: T): T { return JSON.parse(JSON.stringify(value)) }


export function catalogMatchesProject(project: Pick<GovernanceProject,'template_id'|'template_version'>, version?: Pick<TemplateCatalogVersion,'template_id'|'version'>): boolean {
 return !!version && project.template_id === version.template_id && project.template_version === version.version
}

// Replay fixtures are server-registered and immutable. The picker helpers keep
// the exact reviewed pins visible so a curator never has to invent fixture IDs.
export function fixtureCoverageKey(fixture: Pick<ReplayFixtureSummary, 'rule_id' | 'scene' | 'polarity'>): string {
  return `${fixture.rule_id}/${fixture.scene}/${fixture.polarity}`
}
export interface FixtureCoverageReport {
  // true when this client cannot enumerate the required combinations (catalog
  // unreadable or no registered fixture for the current target). Callers must
  // then say 待服务端核验 instead of presenting 0/0 as complete coverage.
  unknown: boolean
  // unique rule/scene/polarity combinations required by the registered fixtures
  required: number
  covered: number
  missing: string[]
  // registered fixtures beyond the first one of each unique combination
  extra: number
}
export function fixtureCoverage(selectedIds: string[], fixtures: ReplayFixtureSummary[] | null | undefined): FixtureCoverageReport {
  if (!fixtures || fixtures.length === 0) {
    return { unknown: true, required: 0, covered: 0, missing: [], extra: 0 }
  }
  const comboIds = new Map<string, string[]>()
  for (const fixture of fixtures) {
    const key = fixtureCoverageKey(fixture)
    comboIds.set(key, [...(comboIds.get(key) || []), fixture.fixture_id])
  }
  const selected = new Set(selectedIds)
  const missing: string[] = []
  for (const [key, ids] of comboIds) {
    // Any registered fixture of a combination satisfies it; duplicates must not
    // inflate the requirement.
    if (!ids.some(id => selected.has(id))) {
      missing.push(key)
    }
  }
  const required = comboIds.size
  return { unknown: false, required, covered: required - missing.length, missing, extra: fixtures.length - required }
}
export interface ReplayTarget { template_id: string; template_version: string }
export function fixturesForReplayTarget(fixtures: ReplayFixtureSummary[], target?: ReplayTarget): ReplayFixtureSummary[] {
 if (!target || !target.template_id.trim() || !target.template_version.trim()) return []
 return fixtures.filter(fixture => fixture.template_id === target.template_id && fixture.template_version === target.template_version)
}

export const replaySceneLabel = (scene: string) => scene === 'write_check' ? '写作检查' : scene === 'preflight' ? '交付预检' : scene
export const replayPolarityLabel = (polarity: string) => polarity === 'positive' ? '正例' : polarity === 'negative' ? '反例' : polarity

// Local pre-validation only: the server remains the authority on shape,
// semantics and the trial replay. This catches unreadable batches before a
// doomed request and keeps the JSON the curator typed intact for fixing.
export function parseFixtureBatch(text: string): ReplayFixtureInput[] {
 const trimmed = text.trim()
 if (!trimmed) throw new Error('请先粘贴要登记的夹具 JSON 数组。')
 let parsed: unknown
 try { parsed = JSON.parse(trimmed) } catch { throw new Error('夹具登记内容不是可解析的 JSON；请修正后再提交，原输入已保留。') }
 if (!Array.isArray(parsed) || !parsed.length) throw new Error('夹具登记须是非空 JSON 数组；单批条数上限以服务器约定为准。')
 return parsed as ReplayFixtureInput[]
}

// Coverage matrix rows in binding order for display: each line names the rule
// and scene, whether positive/negative examples cover it, and which declared
// example references the registered fixture set does not satisfy yet.
export interface ReplayCoverageRow {
 key: string
 text: string
 missing: boolean
}
export function replayCoverageRows(matrix: Pick<ReplayCoverageMatrix, 'entries'>): ReplayCoverageRow[] {
 return matrix.entries.map(entry => ({
  key: `${entry.rule_id}/${entry.scene}`,
  missing: !entry.positive_covered || !entry.negative_covered || entry.unsatisfied_declared_example_refs.length > 0,
  text: `${entry.rule_id} · ${replaySceneLabel(entry.scene)} · ${replayPolarityLabel('positive')}${entry.positive_covered ? '已覆盖' : '缺失'} · ${replayPolarityLabel('negative')}${entry.negative_covered ? '已覆盖' : '缺失'}` +
   (entry.unsatisfied_declared_example_refs.length ? ` · 声明例未满足：${entry.unsatisfied_declared_example_refs.join('、')}` : ''),
 }))
}

// Replay base selection for ruleset-projection registration (Spec #768 D3/D12).
// The guided path pins the first organizational rule set to the builtin demo
// template, so that choice is the panel's default; any catalog version the
// manager can read stays selectable through the same encoded value.
export interface ReplayBaseChoice { template_id: string; template_version: string }
export const demoReplayBase: ReplayBaseChoice = { template_id: 'template-demo', template_version: '1' }
export function replayBaseChoiceValue(choice: ReplayBaseChoice): string {
 return `${choice.template_id}|${choice.template_version}`
}
export function parseReplayBaseChoice(value: string): ReplayBaseChoice {
 const at = value.indexOf('|')
 const template_id = at <= 0 ? '' : value.slice(0, at).trim()
 const template_version = at >= 0 && at < value.length - 1 ? value.slice(at + 1).trim() : ''
 if (!template_id || !template_version) throw new Error('请选择回放底版模板与精确版本。')
 return { template_id, template_version }
}

// One shared flattening: a registration receipt and a coverage readback feed
// the same matrix display, so the panel never re-derives the view per spelling.
// `projection` stays undefined on the template-path spelling.
export function coverageViewFromReceipt(receipt: RegisterReplayFixturesResult): ReplayCoverageView {
 return {
  template_id: receipt.template_id, version: receipt.version, content_hash: receipt.content_hash, policy_hash: receipt.policy_hash,
  complete: receipt.coverage.complete, entries: receipt.coverage.entries,
  unsatisfied_declared_example_refs: receipt.coverage.unsatisfied_declared_example_refs, projection: receipt.projection,
 }
}
export function projectionSummary(projection: RulesetProjectionRef): string {
 return `投影规则集 ${projection.ruleset.ruleset_id} / ${projection.ruleset.version} · 回放底版 ${projection.replay_base.template_id} / ${projection.replay_base.version}`
}

// request.ts rejects JSON payloads with a non-enumerable $httpStatus, rather
// than necessarily an Error instance. Keep transport failures and CAS conflicts
// distinct without echoing arbitrary server response fields into the page.
const governanceEvidenceCodes: Record<string, string> = {
 // 400 boundary: everything decidable without running the evaluator.
 invalid_request: '登记或请求内容不符合约定（400）',
 // 422 family: the trial replay rejected the declared evidence.
 template_replay_required: '回放核验未通过（422）',
 // Other governed decisions the panel can act on.
 template_governance_unavailable: '目录治理服务暂不可用（503）',
 human_decision_required: '该治理决定需要人工账号（403）',
}
export function governanceOperationError(error: unknown): string {
 const fallback = '服务请求失败，请重新读取状态后复核。'
 if (error && typeof error === 'object') {
  const payload = error as Record<string, unknown>
  const nested = payload.error && typeof payload.error === 'object' ? payload.error as Record<string, unknown> : undefined
  const code = typeof nested?.code === 'string' && nested.code ? nested.code : typeof payload.code === 'string' ? payload.code : ''
  if (code === 'idempotency_conflict') return '同一幂等键已对应不同请求；请复核内容后更换新的提交。'
  if (code === 'version_conflict' || payload.$httpStatus === 409 || payload.status === 409) {
   return '内容或基线已更新（版本冲突）。请先重新读取版本，再核对保留的编辑。'
  }
  // Known governance codes carry readable evidence: echo the server's own
  // message behind a recognized label; unknown codes stay behind the fallback
  // so arbitrary response fields never reach the page.
  const evidenceLabel = governanceEvidenceCodes[code]
  if (evidenceLabel) {
   const detail = typeof payload.message === 'string' && payload.message.trim() ? payload.message
    : typeof nested?.message === 'string' && nested.message.trim() ? nested.message : ''
   return detail ? `${evidenceLabel}：${detail}` : `${evidenceLabel}。`
  }
 }
 return error instanceof Error ? error.message : fallback
}
