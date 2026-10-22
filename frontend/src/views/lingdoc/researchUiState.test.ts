import test from 'node:test'
import assert from 'node:assert/strict'
import { IdempotencyKeyCache, ResponseEpoch, agentSpecCandidateBasisCurrent, acceptedAgentSpecCandidate, buildAgentSpecCandidateInput, eligibleAgentSpecTurn, pinAgentSpecCandidateBasis, agentSpecRunModeLabel, nonemptyResearchQueries, safeResearchUrl, selectedRuneRange } from './researchUiState'

test('选文坐标使用 Unicode 字符而非 UTF-16 单元，并保留重复片段的实际位置', () => {
  assert.deepEqual(selectedRuneRange('😀甲乙甲乙', '😀甲乙', '甲乙'), { start: 3, end: 5, text: '甲乙' })
  assert.equal(selectedRuneRange('原文', '伪造前缀', '文'), null)
  assert.equal(selectedRuneRange('原文', '原', '不是原文'), null)
  assert.equal(selectedRuneRange('原文', '', ''), null)
})
test('查询保留人工输入顺序，不由界面补造查询', () => {
  assert.deepEqual(nonemptyResearchQueries(' 定义 \r\n\n 反例 \n'), ['定义', '反例'])
  assert.deepEqual(nonemptyResearchQueries(''), [])
})
test('外部来源链接拒绝脚本、凭据和无效 URL', () => {
  for (const value of ['javascript:alert(1)', 'data:text/html,abc', 'https://user:password@example.org/', 'not-a-link']) assert.equal(safeResearchUrl(value), null)
  assert.equal(safeResearchUrl('https://example.org/paper?q=1'), 'https://example.org/paper?q=1')
})
test('不确定请求复用相同幂等键，用户改变实际版本或范围才产生新键', () => {
  let sequence = 0
  const keys = new IdempotencyKeyCache(() => 'operation-' + ++sequence)
  const input = { expected_version: 2, model_use_allowed: false, source_ids: ['source'] }
  assert.equal(keys.forRequest('turn', input), 'operation-1')
  assert.equal(keys.forRequest('turn', { ...input }), 'operation-1')
  assert.equal(keys.forRequest('turn', { ...input, expected_version: 3 }), 'operation-2')
  keys.accepted('turn')
  assert.equal(keys.forRequest('turn', input), 'operation-3')
  keys.clear()
  assert.equal(keys.forRequest('turn', input), 'operation-4')
})
test('项目或调用者变更使旧异步响应失效', () => {
  const scope = new ResponseEpoch(), original = scope.capture()
  assert.equal(scope.accepts(original), true)
  scope.next()
  assert.equal(scope.accepts(original), false)
  assert.equal(scope.accepts(scope.capture()), true)
})

const specProject=()=>({id:'p',status:'draft',project_version:7,spec_revision:3,current_context_revision:4,spec:{topic:'当前值',goal:'保留',extra:'保留孤立项'},template_id:'t',template_version:'1',template_copy_version:1,template_copy:{id:'copy',version:1,content_hash:'hash',definition:{fields:[{field_id:'topic',label:'主题',type:'string',required:true}]}}}) as unknown as import('../../api/lingdoc/workspace').Project
const specTurn=()=>({id:'turn',project_id:'p',session_id:'session',version:2,status:'completed',reply:'原样值',created_by:'u'}) as import('../../api/lingdoc/agentApi').AgentTurn
test('研究条件候选全字段 merge，只绑定所选当前字段的真实轮次来源，无客户端许可 flags',()=>{
 const p=specProject(),t=specTurn(),input=buildAgentSpecCandidateInput(p,t,'topic',' 原样值 ')
 assert.deepEqual(input.fields,{topic:'原样值',goal:'保留',extra:'保留孤立项'})
 assert.deepEqual(input.field_metadata.topic,{origin:'ai_generated',status:'pending_confirmation',provenance:{source_type:'agent_turn',source_id:'turn',source_version:'2',purpose:'project_spec',locator:'reply'}})
 assert.equal(buildAgentSpecCandidateInput(p,t,'topic','提取后人工编辑').field_metadata.topic!.origin,'ai_assisted_human')
 assert.throws(()=>buildAgentSpecCandidateInput(p,t,'fake-field','x'))
 assert.throws(()=>buildAgentSpecCandidateInput({...p,status:'active'},t,'topic','x'))
 assert.throws(()=>buildAgentSpecCandidateInput(p,{...t,version:0},'topic','x'))
 assert.throws(()=>buildAgentSpecCandidateInput(p,t,'topic','中'.repeat(3334)),/UTF-8/)
 assert.equal(p.spec.topic,'当前值')
})
test('研究条件候选固定 PV/Spec/模板/调用者/tenant，普通同值对象刷新和字段顺序不跳基线',()=>{
 const p=specProject(),basis=pinAgentSpecCandidateBasis(p,'u','1')
 assert.equal(agentSpecCandidateBasisCurrent(basis,{...p,spec:{extra:'保留孤立项',goal:'保留',topic:'当前值'}},'u','1'),true)
 for(const next of [{...p,project_version:8},{...p,spec_revision:4},{...p,current_context_revision:5},{...p,template_copy_version:2},{...p,template_copy:{...p.template_copy!,content_hash:'changed'}}]){
  assert.equal(agentSpecCandidateBasisCurrent(basis,next,'u','1'),false)
 }
 assert.equal(agentSpecCandidateBasisCurrent(basis,p,'other','1'),false);assert.equal(agentSpecCandidateBasisCurrent(basis,p,'u','2'),false)
})
test('来源入口匹配真实当前 turn/session 作者与正版本；完整/no-op 回执核验相同来源和状态',()=>{
 const p=specProject(),t=specTurn(),session={id:'session',project_id:'p',version:1,created_by:'u'} as import('../../api/lingdoc/agentApi').AgentSession
 assert.equal(eligibleAgentSpecTurn(t,session,p,'u'),true)
 assert.equal(eligibleAgentSpecTurn(t,{...session,created_by:'other'},p,'u'),false)
 assert.equal(eligibleAgentSpecTurn({...t,reply:''},session,p,'u'),false)
 const input=buildAgentSpecCandidateInput(p,t,'topic','原样值'),result={...p,spec_revision:4,spec:input.fields,spec_fields:{topic:{value:'原样值',...input.field_metadata.topic!,modified_by:'u',modified_at:'now',provenance:{...input.field_metadata.topic!.provenance,created_by:'u',accessible:true,needs_review:false}}}}
 assert.equal(acceptedAgentSpecCandidate(result,'p',input),true)
 assert.equal(acceptedAgentSpecCandidate({...result,spec_revision:3},'p',input),true)
 assert.equal(acceptedAgentSpecCandidate({...result,spec_revision:2},'p',input),false)
 assert.equal(acceptedAgentSpecCandidate({...result,spec_fields:undefined},'p',input),false)
 assert.equal(acceptedAgentSpecCandidate({...result,id:'other'},'p',input),false)
 assert.equal(acceptedAgentSpecCandidate({...result,spec:{...result.spec,goal:'被遗漏'}},'p',input),false)
 assert.equal(agentSpecRunModeLabel('mock'),'模拟回复，未调用真实模型')
 assert.match(agentSpecRunModeLabel('real_api_fake_model'),/非真实模型/)
})
