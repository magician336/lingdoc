import test from 'node:test'
import assert from 'node:assert/strict'
import { assistantAffectedGroups, assistantAffectedObjectLabel, assistantAffectedBasisLabel } from './assistantAffectedGroups'
import type { AssistantAffectedExplanation } from '@/api/lingdoc/workspace'

const explanation: AssistantAffectedExplanation = {
  definite_affected: [
    {
      object: { kind: 'ChapterVersion', id: 'chapter-1', version: 'v1' },
      reason: '该章正文引用了被修改的研究条件',
      basis: { source_type: 'dependency_edge', source_id: 'edge-1' },
    },
  ],
  possible_affected: [
    {
      object: { kind: 'EvidenceCard', id: 'card-1', version: '2' },
      reason: '证据卡适用范围可能覆盖新条件',
      basis: { source_type: 'ruleset', source_id: 'ruleset-1', detail: '适用范围规则' },
    },
  ],
}

test('两组说明各自带标题、对象与依据', () => {
  const groups = assistantAffectedGroups(explanation)
  assert.equal(groups.length, 2)
  assert.equal(groups[0].group, 'definite_affected')
  assert.equal(groups[0].title, '确定受影响')
  assert.equal(groups[1].group, 'possible_affected')
  assert.equal(groups[1].title, '可能受影响')
  assert.equal(assistantAffectedObjectLabel(groups[0].entries[0]), 'ChapterVersion · chapter-1 · v1')
  assert.equal(assistantAffectedBasisLabel(groups[0].entries[0]), 'dependency_edge · edge-1')
  assert.equal(assistantAffectedBasisLabel(groups[1].entries[0]), 'ruleset · ruleset-1 · 适用范围规则')
})

test('没有说明时不渲染任何分组', () => {
  assert.deepEqual(assistantAffectedGroups(undefined), [])
  assert.deepEqual(assistantAffectedGroups(null), [])
  assert.deepEqual(assistantAffectedGroups({ definite_affected: [], possible_affected: [] }), [])
})

test('助手侧不出现影响解析的 certainty 词汇', () => {
  const rendered = JSON.stringify(assistantAffectedGroups(explanation))
  assert.equal(rendered.includes('certainty'), false)
})
