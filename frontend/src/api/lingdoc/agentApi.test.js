import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'

function fixture() {
 const calls = []
 const request = Object.fromEntries(['get','post','put'].map(method => [method, (...args) => { calls.push({ method, args }); return Promise.resolve({ data: {} }) }]))
 const source = fs.readFileSync(new URL('./agentApi.ts', import.meta.url), 'utf8')
 const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
 const exports = {}
 vm.runInNewContext(output, { exports, require: id => { if (id === '@/utils/request') return request; throw new Error('unexpected dependency: ' + id) } })
 return { api: exports, calls }
}
test('实际调研任务路径编码对象编号、保留版本与 Idempotency-Key', async () => {
 const { api, calls } = fixture()
 const input = { task_id: 'task/1', expected_project_version: 7, expected_context_revision: 4, expected_version: 2, queries: ['定义','反例'], asset_ids: ['authorized-asset'] }
 await api.saveResearchTask('project/1', input, 'same-key')
 assert.equal(calls[0].method, 'put')
 assert.equal(calls[0].args[0], '/api/v1/lingdoc/projects/project%2F1/research-tasks/task%2F1')
 assert.equal(calls[0].args[1], input)
 assert.equal(calls[0].args[2].headers['Idempotency-Key'], 'same-key')
})
test('上下文预览只用 POST body，不把来源或敏感同意放入 URL', async () => {
 const { api, calls } = fixture()
 const input = { source_ids: ['private-source'], card_refs: [], sensitive_context_confirmed: false, expected_context_revision: 9 }
 await api.previewAgentContext('project', input)
 assert.equal(calls[0].method, 'post')
 assert.equal(calls[0].args[0], '/api/v1/lingdoc/projects/project/agent-context')
 assert.equal(calls[0].args[1], input)
 assert.equal(input.sensitive_context_confirmed, false)
})
test('导入与动作 API 不代用户授予模型使用或明确确认', async () => {
 const { api, calls } = fixture()
 const candidate = { scope: 'personal', model_use_allowed: false, save_mode: 'metadata_only' }
 await api.createImportCandidate('p', candidate, 'import-key')
 const confirmation = { confirmed: false, reason: '', expected_version: 3 }
 await api.confirmActionProposal('p', 'action/1', confirmation, 'action-key')
 assert.equal(calls[0].args[1].model_use_allowed, false)
 assert.equal(calls[1].args[1].confirmed, false)
 assert.equal(calls[1].args[0], '/api/v1/lingdoc/projects/p/action-proposals/action%2F1/confirm')
})
test('控制只传明确动作且不发第二次模型或查询请求', async () => {
 const { api, calls } = fixture()
 const input = { action: 'pause', reason: '用户暂停', expected_version: 6 }
 await api.controlResearchTask('p', 't', input, 'control-key')
 assert.equal(calls.length, 1)
 assert.equal(calls[0].args[0], '/api/v1/lingdoc/projects/p/research-tasks/t/control')
 assert.equal(calls[0].args[1], input)
})
test('原文检查、会话和长请求路径遵从同一真实资源编号', async () => {
 const { api, calls } = fixture()
 await api.inspectResearchResult('p', 'run/1', 'result/1')
 await api.sendAgentTurn('p', 'session/1', { user_input: '候选建议', sensitive_context_confirmed: false }, 'turn-key')
 assert.equal(calls[0].args[0], '/api/v1/lingdoc/projects/p/research-runs/run%2F1/results/result%2F1/inspect')
 assert.equal(calls[1].args[0], '/api/v1/lingdoc/projects/p/agent-sessions/session%2F1/turns')
 assert.equal(calls[1].args[2].timeout, 120000)
 assert.equal(calls[1].args[2].headers['Idempotency-Key'], 'turn-key')
})

test('发一轮只发用户输入与上下文，不附带提案字段，也不替用户写调研任务',async()=>{
 const {api,calls}=fixture()
 const input={user_input:'帮我找合成材料的资料',source_ids:[],card_refs:[],sensitive_context_confirmed:false,expected_project_version:1,expected_context_revision:1,expected_version:0}
 await api.sendAgentTurn('p','session/1',input,'turn-key')
 assert.equal(calls.length,1)
 assert.equal(calls[0].args[1],input)
 assert.equal(JSON.stringify(calls[0].args[1]).includes('task_card'),false)
 assert.equal(calls.some(call=>call.method==='saveResearchTask'),false)
})

test('资产范围调整只起草治理变更，保留原生资料版本且不生成 KB grant',async()=>{
 const {api,calls}=fixture()
 const input={expected_project_version:8,expected_context_revision:5,expected_version:2,confirmed:true,reason:'人工范围决定',policy:{scope:'personal',purpose:'原文复核',allowed_user_ids:[],model_use_allowed:false}}
 await api.proposeProjectAssetPolicy('p','asset/1',input,'policy-key')
 assert.equal(calls[0].args[0],'/api/v1/lingdoc/projects/p/assets/asset%2F1/policy/proposals')
 assert.equal(calls[0].args[1],input)
 assert.equal(calls[0].args[1].expected_version,2)
 assert.equal(calls[0].args[1].policy.model_use_allowed,false)
 assert.equal(calls.length,1)
})

test('会话字段候选使用真实 Spec PUT 契约和精确键，不发模型或 activation 请求',async()=>{
 const {api,calls}=fixture(),input={expected_spec_revision:4,fields:{topic:'候选',goal:'保留'},field_metadata:{topic:{origin:'ai_generated',status:'pending_confirmation',provenance:{source_type:'agent_turn',source_id:'actual-turn',source_version:'2',purpose:'project_spec',locator:'reply'}}}}
 await api.saveAgentSpecCandidate('project/1',input,'same-save-key')
 assert.equal(calls.length,1);assert.equal(calls[0].method,'put')
 assert.equal(calls[0].args[0],'/api/v1/lingdoc/projects/project%2F1/spec');assert.equal(calls[0].args[1],input)
 assert.equal(calls[0].args[2].headers['Idempotency-Key'],'same-save-key');assert.equal('expected_project_version' in input,false)
 await api.getAgentSpecCandidateProject('project/1');assert.equal(calls[1].method,'get');assert.equal(calls[1].args[0],'/api/v1/lingdoc/projects/project%2F1')
})
