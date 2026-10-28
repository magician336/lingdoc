import assert from 'node:assert/strict'
import test from 'node:test'
import { reactive } from 'vue'
import type { GovernanceProject, ReplayFixtureSummary, TemplateCatalogVersion } from '@/api/lingdoc/templateGovernance'
import {
 catalogMatchesProject, cloneDraftInput, coverageViewFromReceipt, demoReplayBase, fixtureCoverage, governanceOperationError, fixturesForReplayTarget, mutationAttempt, parseFixtureBatch, parseReplayBaseChoice, parseSourceRefs, projectBasis, projectRuleSet, projectionSummary,
 replayBaseChoiceValue, replayCoverageRows, runModeLabel, splitIds, stateLabel, suggestionBindings,
} from './templateGovernanceState'

test('unknown server states are visible and never labeled published or successful', () => {
 assert.equal(stateLabel('queued_by_new_provider'), '待识别状态（queued_by_new_provider）')
 assert.equal(runModeLabel('real_api_fake_model'), '真实接口 / 模拟模型')
 assert.equal(runModeLabel(undefined), '运行方式未确认')
})
test('draft extraction accepts Vue proxy data and strips immutable hash and compiled rules', () => {
 const version = reactive({
 template_id: 'template-1', version: '2', revision: 3, state: 'draft',
 definition: { id: 'template-1', version: '2', name: 'Reviewed template', fields: [{ field_id: 'goal', label: 'Goal', type: 'enum', required: true, options: ['a', 'b'] }], sections: [], terms: [], required_fields: ['goal'], rules: [{ evaluator: 'server-owned' }], content_hash: 'immutable', ruleset_hash: 'compiled' },
 ruleset: { ruleset_id: 'rules-1', version: '2', ruleset_hash: 'version-hash' },
 binding_policies: [{rule_id:'goal-present',may_disable:true,allowed_severities:['warning']}], policy_hash:'server-policy-hash',
 source_type: 'organization_input', scope: 'project', source_refs: [{ source_id: 'source-1', source_version: '3' }], curator: 'reviewer',
 }) as unknown as TemplateCatalogVersion
 const draft = cloneDraftInput(version)
 assert.equal(draft.expected_revision, 3)
 assert.equal('rules' in draft.definition, false)
 assert.equal('content_hash' in draft.definition, false)
 draft.definition.fields[0].options?.push('c')
 draft.source_refs[0].source_id = 'changed'
 assert.deepEqual(version.definition.fields[0].options, ['a', 'b'])
 assert.equal(version.source_refs[0].source_id, 'source-1')
 draft.binding_policies![0].allowed_severities!.push('info')
 assert.deepEqual(version.binding_policies[0].allowed_severities,['warning'])
 assert.equal('policy_hash' in draft,false)
})
test('project basis preserves independent snapshot refs and does not invent missing rule identity', () => {
 const project = {
 id: 'p', name: 'project', status: 'active', project_version: 3, spec_revision: 2, current_context_revision: 1,
 spec: {}, template_id: 'template-1', template_version: '1', template_copy_version: 1, members: [],
 template_copy: { ruleset_hash: 'effective-hash', rule_set_snapshot: { ruleset_id: 'project-rules', version: 'copy-1', ruleset_hash: 'effective-hash', state: 'published', bindings: [] } },
 } as unknown as GovernanceProject
 assert.deepEqual(projectRuleSet(project), { ruleset_id: 'project-rules', version: 'copy-1', ruleset_hash: 'effective-hash' })
 assert.equal(projectBasis(project).current_snapshot_id, '')
 const missing = { ...project, template_copy: undefined }
 assert.equal(projectRuleSet(missing).ruleset_id, '')
 assert.equal(projectRuleSet(missing).version, '')
})
test('source refs require actual version syntax and preserve identities containing at signs', () => {
 assert.deepEqual(parseSourceRefs('asset@locator@4\nsource-2@8'), [
 { source_id: 'asset@locator', source_version: '4' }, { source_id: 'source-2', source_version: '8' },
 ])
 assert.throws(() => parseSourceRefs('unversioned'), /真实 source_id/)
 assert.deepEqual(splitIds('fixture-a，fixture-b\nfixture-a'), ['fixture-a', 'fixture-b'])
})
test('identical retry keeps its operation key, changed revision or reason gets a new key', () => {
 let minted = 0
 const mint = () => `key-${++minted}`
 const input = { expected_revision: 3, reason: 'human reviewed' }
 const first = mutationAttempt(undefined, input, mint)
 assert.equal(mutationAttempt(first, { ...input }, mint), first)
 assert.notEqual(mutationAttempt(first, { ...input, expected_revision: 4 }, mint).key, first.key)
 assert.notEqual(mutationAttempt(first, { ...input, reason: 'another decision' }, mint).key, first.key)
})
test('human suggestion draft clones parameters and starts nonblocking without publishing', () => {
 const run = { actor_id: 'reviewer', output: { proposals: [{ rule_id: 'new-rule', definition_id: 'pattern', definition_version: '1',
 target_kind: 'field', target_id: 'goal', parameters: { pattern: '^Study' }, message: 'Review goal wording' }] } }
 const bindings = suggestionBindings(run as any)
 assert.equal(bindings[0].severity, 'info')
 assert.equal(bindings[0].origin, 'project')
 bindings[0].parameters!.pattern = '^Changed'
 assert.equal(run.output.proposals[0].parameters.pattern, '^Study')
 assert.deepEqual(bindings[0].positive_example_refs, [])
})


test('a suggestion cannot mix the current project context with another catalog version', () => {
 const project={template_id:'current-template',template_version:'2'}
 assert.equal(catalogMatchesProject(project,{template_id:'current-template',version:'2'}),true)
 assert.equal(catalogMatchesProject(project,{template_id:'current-template',version:'1'}),false)
 assert.equal(catalogMatchesProject(project,{template_id:'another-template',version:'2'}),false)
 assert.equal(catalogMatchesProject(project,undefined),false)
})
test('fixture coverage counts unique required combinations and any registered fixture of a combination satisfies it', () => {
 const fixture = (id: string, rule: string, scene: string, polarity: string) => (
  { fixture_id: id, rule_id: rule, scene, polarity, target_ref: 'project/p', ruleset: { ruleset_id: 'r', version: '1' }, template_id: 'template-demo', template_version: '1' } as ReplayFixtureSummary
 )
 const fixtures = [
  fixture('demo-a-write_check-positive', 'a', 'write_check', 'positive'),
  fixture('demo-a-write_check-negative', 'a', 'write_check', 'negative'),
  fixture('demo-b-preflight-positive', 'b', 'preflight', 'positive'),
 ]
 const full = fixtureCoverage(fixtures.map(item => item.fixture_id), fixtures)
 assert.equal(full.unknown, false)
 assert.equal(full.required, 3)
 assert.equal(full.covered, 3)
 assert.deepEqual(full.missing, [])
 // Two registered fixtures of the same combination: selecting one satisfies
 // the combination instead of displaying false missing coverage.
 const duplicated = [...fixtures, fixture('demo-a-write_check-positive-alt', 'a', 'write_check', 'positive')]
 const oneOfPair = fixtureCoverage(['demo-a-write_check-positive-alt', 'demo-a-write_check-negative', 'demo-b-preflight-positive'], duplicated)
 assert.equal(oneOfPair.required, 3)
 assert.equal(oneOfPair.covered, 3)
 assert.deepEqual(oneOfPair.missing, [])
 assert.equal(oneOfPair.extra, 1)
 // Partial coverage names the missing combination, not the missing fixture.
 const partial = fixtureCoverage(['demo-a-write_check-positive'], fixtures)
 assert.equal(partial.covered, 1)
 assert.deepEqual(partial.missing, ['a/write_check/negative', 'b/preflight/positive'])
 // Unknown IDs never count toward coverage.
 const withUnknown = fixtureCoverage([...fixtures.map(item => item.fixture_id), 'made-up-id'], fixtures)
 assert.equal(withUnknown.covered, 3)
 assert.deepEqual(withUnknown.missing, [])
})
test('fixture coverage is explicitly unknown for an unreadable or empty catalog instead of faking 0/0 completeness', () => {
 const empty = fixtureCoverage(['hand-typed-id'], [])
 assert.equal(empty.unknown, true)
 assert.equal(empty.required, 0)
 assert.equal(empty.covered, 0)
 assert.deepEqual(empty.missing, [])
 const unreadable = fixtureCoverage(['hand-typed-id'], null)
 assert.equal(unreadable.unknown, true)
 const undefinedCatalog = fixtureCoverage([], undefined)
 assert.equal(undefinedCatalog.unknown, true)
})
test('fixtures filter to the exact replay target and never leak across versions', () => {
 const fixtures = [
  { fixture_id: 'demo-a', rule_id: 'a', scene: 'write_check', polarity: 'positive', target_ref: 't', ruleset: { ruleset_id: 'r', version: '1' }, template_id: 'template-demo', template_version: '1' },
  { fixture_id: 'demo-b', rule_id: 'a', scene: 'write_check', polarity: 'negative', target_ref: 't', ruleset: { ruleset_id: 'r', version: '1' }, template_id: 'template-demo', template_version: '2' },
 ] as ReplayFixtureSummary[]
 assert.deepEqual(fixturesForReplayTarget(fixtures, { template_id: 'template-demo', template_version: '1' }).map(fixture => fixture.fixture_id), ['demo-a'])
 assert.deepEqual(fixturesForReplayTarget(fixtures, { template_id: 'template-demo', template_version: '2' }).map(fixture => fixture.fixture_id), ['demo-b'])
 assert.deepEqual(fixturesForReplayTarget(fixtures, undefined), [])
 assert.deepEqual(fixturesForReplayTarget(fixtures, { template_id: '', template_version: '1' }), [])
})

test('governance conflict copy recognizes the actual JSON error envelope and non-enumerable status', () => {
 const response = Object.defineProperty({ error: { code: 'version_conflict' } }, '$httpStatus', { value: 409 })
 assert.match(governanceOperationError(response), /版本冲突.*重新读取版本/)
 assert.match(governanceOperationError({ code: 'version_conflict' }), /版本冲突/)
 assert.match(governanceOperationError(Object.defineProperty({}, '$httpStatus', { value: 409 })), /版本冲突/)
})
test('governance transport errors stay distinct and do not echo unknown response fields', () => {
 const error = { message: 'password=private-transport-detail', error: { code: 'unrecognized' } }
 assert.equal(governanceOperationError(error), '服务请求失败，请重新读取状态后复核。')
 assert.doesNotMatch(governanceOperationError(error), /版本冲突|password/)
 assert.equal(governanceOperationError(new Error('声明式参数不是有效 JSON')), '声明式参数不是有效 JSON')
})
test('fixture batch parsing keeps unreadable JSON local and passes arrays through untouched', () => {
 const batch = [{ fixture_id: 'org-a', rule_id: 'goal-present', scene: 'write_check', polarity: 'positive', target_ref: 'field/goal',
  input: { project_id: 'replay-synthetic', target_version: '1', fields: { goal: '目标已确认' } }, expected_status: 'PASS', review_reference: 'review-record-2026-001' }]
 assert.deepEqual(parseFixtureBatch(JSON.stringify(batch)), batch)
 assert.throws(() => parseFixtureBatch('   '), /请先粘贴要登记的夹具/)
 assert.throws(() => parseFixtureBatch('{broken'), /不是可解析的 JSON/)
 assert.throws(() => parseFixtureBatch('{"fixture_id":"org-a"}'), /非空 JSON 数组/)
 assert.throws(() => parseFixtureBatch('[]'), /非空 JSON 数组/)
})
test('coverage rows name the rule, scene and polarity gaps in binding order', () => {
 const rows = replayCoverageRows({ entries: [
  { rule_id: 'goal-present', scene: 'write_check', positive_covered: true, negative_covered: false, declared_positive_example_refs: [], declared_negative_example_refs: [], unsatisfied_declared_example_refs: [] },
  { rule_id: 'source-fresh', scene: 'preflight', positive_covered: true, negative_covered: true, declared_positive_example_refs: ['ex-1'], declared_negative_example_refs: [], unsatisfied_declared_example_refs: ['ex-1'] },
 ] })
 assert.equal(rows.length, 2)
 assert.equal(rows[0].missing, true)
 assert.match(rows[0].text, /goal-present · 写作检查 · 正例已覆盖 · 反例缺失/)
 assert.equal(rows[1].missing, true)
 assert.match(rows[1].text, /source-fresh · 交付预检 · 正例已覆盖 · 反例已覆盖/)
 assert.match(rows[1].text, /声明例未满足：ex-1/)
 const complete = replayCoverageRows({ entries: [{ rule_id: 'a', scene: 'write_check', positive_covered: true, negative_covered: true, declared_positive_example_refs: [], declared_negative_example_refs: [], unsatisfied_declared_example_refs: [] }] })
 assert.equal(complete[0].missing, false)
})
test('governance 400/422 evidence echoes known codes with the server message and unknown codes stay generic', () => {
 const replay = Object.defineProperty({ status: 422, message: '夹具 org-a 声明 PASS 但实际求值 ISSUE。', error: { code: 'template_replay_required', message: '须用服务器注册且绑定当前版本的正反例完成回放。', retryable: false }, request_id: 'req-1' }, '$httpStatus', { value: 422 })
 const shown = governanceOperationError(replay)
 assert.match(shown, /回放核验未通过（422）/)
 assert.match(shown, /夹具 org-a 声明 PASS 但实际求值 ISSUE/)
 const invalid = Object.defineProperty({ status: 400, message: '夹具数量超过单批上限。', error: { code: 'invalid_request', message: '请求字段不符合约定。', retryable: false } }, '$httpStatus', { value: 400 })
 const invalidShown = governanceOperationError(invalid)
 assert.match(invalidShown, /登记或请求内容不符合约定（400）/)
 assert.match(invalidShown, /超过单批上限/)
 const idempotent = governanceOperationError({ status: 409, message: '同一幂等键对应不同请求。', error: { code: 'idempotency_conflict', message: '同一幂等键对应不同请求。', retryable: false } })
 assert.match(idempotent, /幂等键/)
 assert.doesNotMatch(idempotent, /版本冲突/)
 // An unknown governance code with an arbitrary message still stays generic.
 assert.equal(governanceOperationError(Object.defineProperty({ status: 422, message: 'internal stack trace', error: { code: 'future_code', message: 'secret detail' } }, '$httpStatus', { value: 422 })), '服务请求失败，请重新读取状态后复核。')
})
test('replay base choice defaults to the demo guided path and rejects incomplete selections', () => {
 assert.equal(replayBaseChoiceValue(demoReplayBase), 'template-demo|1')
 assert.deepEqual(parseReplayBaseChoice('template-demo|1'), demoReplayBase)
 assert.deepEqual(parseReplayBaseChoice('org-template|7'), { template_id: 'org-template', template_version: '7' })
 assert.throws(() => parseReplayBaseChoice('org-template'), /回放底版/)
 assert.throws(() => parseReplayBaseChoice('|1'), /回放底版/)
 assert.throws(() => parseReplayBaseChoice('org-template|'), /回放底版/)
 assert.throws(() => parseReplayBaseChoice('  | 1 '), /回放底版/)
})
test('a registration receipt and the coverage readback feed one matrix view; projection survives only on the ruleset spelling', () => {
 const receipt = { template_id: 'template-demo', version: '1', content_hash: 'sha:c', policy_hash: 'sha:p', registered: [],
  coverage: { complete: true, entries: [], unsatisfied_declared_example_refs: [] } }
 const plain = coverageViewFromReceipt(receipt as any)
 assert.equal(plain.complete, true)
 assert.equal(plain.content_hash, 'sha:c')
 assert.equal(plain.projection, undefined)
 const withProjection = coverageViewFromReceipt({ ...receipt,
  projection: { ruleset: { ruleset_id: 'org-rules', version: '2', ruleset_hash: 'sha:rs' }, replay_base: { template_id: 'template-demo', version: '1' } } } as any)
 assert.deepEqual(withProjection.projection, { ruleset: { ruleset_id: 'org-rules', version: '2', ruleset_hash: 'sha:rs' }, replay_base: { template_id: 'template-demo', version: '1' } })
 assert.match(projectionSummary(withProjection.projection!), /投影规则集 org-rules \/ 2/)
 assert.match(projectionSummary(withProjection.projection!), /回放底版 template-demo \/ 1/)
})
