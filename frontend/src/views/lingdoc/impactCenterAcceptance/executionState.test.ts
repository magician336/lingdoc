import assert from 'node:assert/strict'
import test from 'node:test'
import { G3_FLOW_EXECUTION_STATE, G3_FLOW_STEPS, flowExecutionStates } from './executionState'

test('流程骨架按状态机前置顺序列出每一步', () => {
  assert.equal(G3_FLOW_STEPS.length, 15)
  assert.deepEqual(
    G3_FLOW_STEPS.map((step) => step.index),
    Array.from({ length: 15 }, (_, index) => index + 1),
  )
  for (const step of G3_FLOW_STEPS) {
    assert.ok(step.name.length > 0, `第 ${step.index} 步缺名称`)
    assert.ok(step.method.length > 0, `第 ${step.index} 步缺方法`)
    assert.ok(step.path.length > 0, `第 ${step.index} 步缺路径`)
    assert.ok(step.precondition.length > 0, `第 ${step.index} 步缺前置条件`)
  }
})

test('本票不发起真实调用，每一步都显式标注「未执行」', () => {
  assert.equal(G3_FLOW_EXECUTION_STATE, '未执行')
  const states = flowExecutionStates()
  assert.equal(states.length, 15)
  for (const state of states) {
    assert.equal(state, '未执行')
  }
})

test('影响评估必须排在应用之前，处置必须在稳定化之前', () => {
  const indexOf = (needle: string) => {
    const found = G3_FLOW_STEPS.findIndex((step) => step.path.includes(needle))
    assert.notEqual(found, -1, `缺少步骤：${needle}`)
    return found
  }
  assert.ok(indexOf('/impact-assessment') < indexOf('/apply'))
  assert.ok(indexOf('/impact-tasks/') < indexOf('/stabilize'))
  assert.ok(indexOf('/stabilize') < indexOf('/checks'))
})
