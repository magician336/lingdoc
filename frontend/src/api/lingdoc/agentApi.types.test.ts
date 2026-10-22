import test from 'node:test'
import assert from 'node:assert/strict'
import type { AgentTurn, AssistantTaskSuggestion, ResearchProposal } from './agentApi'

// 这一份断言挂在 AgentTurn 真正用的那个字段上：并集自己好看没有用，
// 轮次上的 task_card 得真的接受助手那张更松的卡片才算数。
type TurnTaskCard = NonNullable<NonNullable<AgentTurn['suggestions']>['task_card']>

// 类型层断言：助手侧那张同名卡片必须能被 task_card 的类型接受。
//
// 它比研究提案更松：服务端不按提案契约校验它，所以查询词可以只有一条、
// 字段可以缺。若前端把 task_card 写成 ResearchProposal（要求 goal、scope、
// queries、expected_output 都在），这张卡片就会在编译期被拒——那正是本用例
// 要守住的事：类型不能谎报，把运行时真实存在的形状挡在门外。
const assistantCard: TurnTaskCard = { goal: '弄清合成材料的定义', queries: ['合成材料 定义'] }

// 服务端校验过的那份形状仍然完整地能被接受。
const researchCard: TurnTaskCard = { goal: '弄清合成材料的定义', scope: '只查项目资料', queries: ['合成材料 定义', '合成材料 反例'], expected_output: '候选资料与反例' }

test('助手那张更松的卡片能被 task_card 的类型接受', () => {
  const card = assistantCard as AssistantTaskSuggestion
  assert.equal(card.goal, '弄清合成材料的定义')
  assert.deepEqual(card.queries, ['合成材料 定义'])
  assert.equal(card.scope, undefined)
})

test('服务端校验过的那份提案形状仍然完整地能被接受', () => {
  const card = researchCard as ResearchProposal
  assert.equal(card.scope, '只查项目资料')
  assert.equal(card.queries.length, 2)
  assert.equal(card.expected_output, '候选资料与反例')
})
