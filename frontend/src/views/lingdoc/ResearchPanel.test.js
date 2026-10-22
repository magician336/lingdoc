import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileScript, compileTemplate } from '@vue/compiler-sfc'
const tick=async()=>{await Vue.nextTick();await new Promise(resolve=>setImmediate(resolve))}
function fixture(options={}){
 const calls=[],cleanup=[],project={id:'project',name:'合成测试',status:'active',project_version:7,spec_revision:3,current_context_revision:4,spec:{topic:'合成主题'},template_id:'template',template_version:'1',members:[{user_id:'owner',role:'owner'}]}
 const props=Vue.reactive({project,readonly:false,currentUserId:'owner',...options.props})
 const defaultAPI=new Proxy({}, {get:(_,method)=>(...args)=>{calls.push({method,args});if(method==='listReleases')return Promise.resolve({data:{items:[]}});return Promise.resolve({data:[]})}})
 const g2=new Proxy(options.api||{}, {get:(target,method)=>method in target?target[method]:defaultAPI[method]})
 const workspace=new Proxy(options.workspace||{}, {get:(target,method)=>method in target?target[method]:defaultAPI[method]})
 const raw=fs.readFileSync(new URL('./ResearchPanel.vue',import.meta.url),'utf8'),parsed=parse(raw)
 assert.equal(parsed.errors.length,0)
 const script=compileScript(parsed.descriptor,{id:'g2-test'})
 const template=compileTemplate({source:parsed.descriptor.template.content,filename:'ResearchPanel.vue',id:'g2-test',compilerOptions:{bindingMetadata:script.bindings}})
 assert.equal(template.errors.length,0)
 const exports={}
 const helperSource=fs.readFileSync(new URL('./researchUiState.ts',import.meta.url),'utf8'),helpers={}
 vm.runInNewContext(ts.transpileModule(helperSource,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText,{exports:helpers,URL,crypto:{randomUUID:()=>String(calls.length)}})
 const affectedSource=fs.readFileSync(new URL('./assistantAffectedGroups.ts',import.meta.url),'utf8'),affected={}
 vm.runInNewContext(ts.transpileModule(affectedSource,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText,{exports:affected,URL})
 const output=ts.transpileModule(script.content,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
 const emitted=[]
 vm.runInNewContext(output,{exports,console,document:{activeElement:null},setTimeout,clearTimeout,require:id=>{
  if(id==='vue')return {...Vue,onBeforeUnmount:callback=>cleanup.push(callback)}
  if(id==='@/stores/auth')return {useAuthStore:()=>({currentUserId:props.currentUserId,effectiveTenantId:'fixture-tenant'})}
  if(id==='@/api/lingdoc/permissions')return {getEffectivePermission:()=>Promise.resolve({data:{allowed:false,capability:'manage',project_version:props.project.project_version,reason_code:'fixture-denied'}})}
  if(id==='@/api/lingdoc/agentApi')return g2
  if(id==='@/api/lingdoc/workspace')return workspace
  if(id==='./researchUiState')return helpers
  if(id==='./assistantAffectedGroups')return affected
  if(['@/api/lingdoc/delivery','@/api/webSearchProvider','@/api/knowledge-base'].includes(id))return defaultAPI
  throw new Error('unexpected dependency '+id)
 }})
 const state=exports.default.setup(props,{expose:()=>{},emit:(...args)=>emitted.push(args)})
 return {state,props,calls,emitted,affected,close:()=>cleanup.forEach(fn=>fn())}
}
function task(){return {id:'task',version:2,goal:'定义与反例',scope:'当前资料',queries:['定义','反例'],exclusions:[],asset_ids:['asset'],status:'draft',basis:{context_revision:4}}}
test('实际组件在明确确认前不发批准写入，确认后保留当前版本',async()=>{
 const f=fixture();await tick()
 f.state.approveTask(task());await tick()
 assert.equal(f.calls.some(call=>call.method==='approveResearchTask'),false)
 await f.state.executeConfirmation()
 assert.equal(f.calls.some(call=>call.method==='approveResearchTask'),false)
 f.state.confirmChecked.value=true;f.state.confirmReason.value='人工核对范围'
 await f.state.executeConfirmation()
 const request=f.calls.find(call=>call.method==='approveResearchTask')
 assert.equal(request.args[2].confirmed,true)
 assert.equal(request.args[2].expected_version,2)
 assert.equal(request.args[2].expected_context_revision,4)
 f.close()
})
test('实际组件 readonly 即使触发操作函数也不能写入',async()=>{
 const f=fixture({props:{readonly:true}});await tick()
 f.state.approveTask(task());f.state.confirmChecked.value=true;f.state.confirmReason.value='不应执行'
 await f.state.executeConfirmation()
 assert.equal(f.calls.some(call=>call.method==='approveResearchTask'),false)
 f.close()
})
test('版本冲突保留用户草稿和原幂等键，不自动重试',async()=>{
 const writes=[]
 const f=fixture({api:{saveResearchTask:(...args)=>{writes.push(args);return Promise.reject({status:409,message:'版本变化'})}}});await tick()
 Object.assign(f.state.taskForm,{goal:'保留这个草稿',queries:'定义\n反例',assets:['asset'],scope:'仅此资料'})
 await f.state.saveTask()
 assert.equal(writes.length,1);assert.equal(f.state.taskForm.goal,'保留这个草稿')
 await f.state.saveTask()
 assert.equal(writes.length,2);assert.equal(writes[0][2],writes[1][2])
 f.close()
})
test('实际来源请求返回前切换项目，旧片段不会进入新项目',async()=>{
 let finish
 const f=fixture({workspace:{getSource:()=>new Promise(resolve=>{finish=resolve})}});await tick()
 const reading=f.state.openSource('private-source')
 f.props.project={...f.props.project,id:'new-project'}
 await tick()
 finish({data:{id:'private-source',quoted_text:'旧项目私有原文',asset_revision:1,status:'available'}})
 await reading
 assert.equal(f.state.source.value,null)
 assert.equal(f.state.sourceId.value,'')
 f.close()
})
test('来源授权失效清除可见片段、助手输入和派生草稿',async()=>{
 const f=fixture({workspace:{getSource:()=>Promise.reject({status:403,message:'来源撤权'})}});await tick()
 f.state.userInput.value='含私有材料的问题';f.state.claimForm.text='派生主张';f.state.cardForm.scope='私有范围'
 await f.state.openSource('revoked')
 assert.equal(f.state.userInput.value,'');assert.equal(f.state.claimForm.text,'');assert.equal(f.state.cardForm.scope,'');assert.equal(f.state.source.value,null)
 f.close()
})

test('unknown 回执不自动重发，旧会话成功记录也不能解除本轮不确定状态',async()=>{
 let count=0
 const old={id:'old-turn',status:'completed',context:{context_revision:4},reply:'以前的回执'}
 const f=fixture({api:{getAgentTurns:()=>Promise.resolve({data:[old]}),sendAgentTurn:()=>{count++;return Promise.reject({status:409,error:{code:'outcome_unknown'},message:'本轮结果未确定'})}}})
 await tick();f.state.sessionId.value='session';await tick()
 f.state.contextForm.consent=true;f.state.userInput.value='保留本轮请求'
 await f.state.sendTurn()
 assert.equal(count,1);assert.equal(f.state.unknown.has('turn-session'),true)
 await f.state.loadTurns()
 assert.equal(f.state.unknown.has('turn-session'),true)
 await f.state.sendTurn()
 assert.equal(count,1);assert.equal(f.state.userInput.value,'保留本轮请求')
 f.close()
})

test('已读取的任务与成功保存的任务不误报未保存，实际编辑才触发 dirty',async()=>{
 const f=fixture({api:{saveResearchTask:(p,input)=>Promise.resolve({data:{...task(),goal:input.goal,question_type:'',expected_output:'候选'}})}});await tick()
 f.state.editTask({...task(),question_type:'',expected_output:'候选'});await tick()
 assert.equal(f.emitted.filter(event=>event[0]==='dirty').at(-1)?.[1],false)
 f.state.taskForm.goal='实际用户修改';await tick()
 assert.equal(f.emitted.filter(event=>event[0]==='dirty').at(-1)?.[1],true)
 await f.state.saveTask();await tick()
 assert.equal(f.emitted.filter(event=>event[0]==='dirty').at(-1)?.[1],false)
 f.close()
})

test('可选页码与段落默认空白，真实定位确认不发送虚构的页码',async()=>{
 const f=fixture();await tick()
 assert.equal(f.state.anchorForm.page,'')
 assert.equal(f.state.anchorForm.paragraph,'')
 f.state.source.value={id:'synthetic-source',locator:'合成原生文本，第0块',asset_revision:1,quoted_text:'合成原文'}
 f.state.selection.value={start:0,end:4,text:'合成原文'}
 f.state.anchorForm.checked=true
 f.state.saveAnchor();await tick()
 assert.equal(f.state.confirming.value.title,'保存原文定位')
 f.state.confirmChecked.value=true;f.state.confirmReason.value='合成定位核对，不虚构页码'
 await f.state.executeConfirmation()
 const request=f.calls.find(call=>call.method==='saveSourceAnchor')
 assert.ok(request)
 assert.equal(request.args[1].page,undefined)
 assert.equal(request.args[1].paragraph,undefined)
 assert.equal(request.args[1].selection_start,0)
 assert.equal(request.args[1].selection_end,4)
 f.close()
})
test('明确填入的页码与段落在真实定位确认中保留',async()=>{
 const f=fixture();await tick()
 f.state.source.value={id:'synthetic-source',locator:'合成文本',asset_revision:1,quoted_text:'合成原文'}
 f.state.selection.value={start:0,end:4,text:'合成原文'}
 Object.assign(f.state.anchorForm,{checked:true,page:2,paragraph:3})
 f.state.saveAnchor();await tick()
 f.state.confirmChecked.value=true;f.state.confirmReason.value='核对明确填写的合成定位'
 await f.state.executeConfirmation()
 const request=f.calls.find(call=>call.method==='saveSourceAnchor')
 assert.ok(request)
 assert.equal(request.args[1].page,2)
 assert.equal(request.args[1].paragraph,3)
 f.close()
})

function changeDraftChapter(project='project',id='chapter-1'){return {id,project_id:project,title:'建议复核 '+id}}
function changeDraftTurn(){return {id:'draft-turn',project_id:'project',session_id:'session',version:2,created_by:'owner',status:'completed',agent:'task_assistant',reply:'合成候选',run_mode:'real_api_fake_model',context:{project_version:7,spec_revision:3,context_revision:4,source_refs:[]},suggestions:{change_draft:{expected_context_revision:4,reason:'明确修改合成研究条件',fields:{topic:'模型拟议主题'},affected_chapter_ids:[]}}}}
test('模型空章节候选先提示人选章，再复用可编辑提案和两次人工确认',async()=>{
 const f=fixture({workspace:{listChapters:project=>Promise.resolve({data:[changeDraftChapter(project),changeDraftChapter(project,'chapter-2')]})},api:{createActionProposal:(project,input)=>{f.calls.push({method:'createActionProposal',args:[project,input]});return Promise.resolve({data:{id:'proposal',version:1,project_id:project,action:input.action,target_id:project,status:'pending',input:input.input,basis:{context_revision:4},source_ids:[],expected_effect:input.expected_effect,reversibility:'仅草稿'}})}}});await tick()
 f.state.prepareAgentChangeDraft(changeDraftTurn());await tick()
 assert.equal(f.calls.some(call=>call.method==='createActionProposal'),false)
 assert.deepEqual([...f.state.changeDraftChapterIDs.value],[])
 const original=f.state.actionForm.changeDraft
 await f.state.proposeAction();await tick()
 assert.equal(f.calls.some(call=>call.method==='createActionProposal'),false)
 assert.equal(f.state.confirming.value,null);assert.match(f.state.error.value,/至少.*一章/)
 assert.equal(f.state.actionForm.changeDraft,original)
 const edit=JSON.parse(original);edit.fields.topic.new_value='人工编辑后的模型候选';edit.reason='人工编辑候选原因';edit.affected_chapter_ids=['ignored-json-chapter'];f.state.actionForm.changeDraft=JSON.stringify(edit)
 f.state.changeDraftChapterIDs.value=['chapter-2']
 await f.state.proposeAction();await tick()
 assert.equal(f.state.error.value,'')
 await f.state.executeConfirmation()
 assert.equal(f.calls.some(call=>call.method==='createActionProposal'),false)
 f.state.confirmChecked.value=true;f.state.confirmReason.value='确认登记待执行提案'
 await f.state.executeConfirmation()
 const request=f.calls.find(call=>call.method==='createActionProposal')
 assert.ok(request);assert.equal(request.args[1].action,'create_change_set');assert.equal(request.args[1].target_id,'project')
 const input=request.args[1].input
 assert.equal(input.fields.topic.old_value,'合成主题');assert.equal(input.fields.topic.new_value,'人工编辑后的模型候选')
 assert.equal(input.reason,'人工编辑候选原因');assert.deepEqual([...input.affected_chapter_ids],['chapter-2'])
 assert.equal(input.field_metadata.topic.origin,'ai_assisted_human');assert.equal(input.field_metadata.topic.provenance.source_id,'draft-turn');assert.equal(input.field_metadata.topic.provenance.source_version,'2')
 assert.equal(input.confirmed,undefined);assert.equal(input.human_confirmed,undefined)
 assert.equal(f.calls.some(call=>call.method==='confirmActionProposal'),false)
 f.state.decideAction({id:'proposal',version:1,action:'create_change_set',status:'pending',basis:{context_revision:4},expected_effect:'只创建草稿',reversibility:'正式版本不变',input},true)
 await f.state.executeConfirmation()
 assert.equal(f.calls.some(call=>call.method==='confirmActionProposal'),false)
 f.state.confirmChecked.value=true;f.state.confirmReason.value='本人确认仅创建草稿'
 await f.state.executeConfirmation()
 const confirm=f.calls.find(call=>call.method==='confirmActionProposal')
 assert.ok(confirm);assert.equal(confirm.args[2].confirmed,true);assert.equal(confirm.args[2].expected_project_version,7);assert.equal(confirm.args[2].expected_version,1)
 assert.equal(f.calls.some(call=>call.method==='applyChangeSet'||call.method==='createChangeSet'),false)
 f.close()
})
test('变更候选不静默重绑陈旧版本，切换操作者清除其来源和草稿',async()=>{
 const f=fixture();await tick();f.state.prepareAgentChangeDraft(changeDraftTurn())
 f.props.project={...f.props.project,project_version:8};await tick();await f.state.proposeAction()
 assert.equal(f.calls.some(call=>call.method==='createActionProposal'),false)
 assert.match(f.state.error.value,/陈旧/)
 f.props.currentUserId='other-user';await tick()
 assert.equal(f.state.changeDraftTurn.value,null);assert.equal(f.state.actionForm.changeDraft,'')
 f.close()
})

test('切换项目清除人工选章，旧项目确认闭包不能提交',async()=>{
 const f=fixture({workspace:{listChapters:project=>Promise.resolve({data:[changeDraftChapter(project,project+'-chapter')]})}});await tick()
 f.state.prepareAgentChangeDraft(changeDraftTurn());f.state.changeDraftChapterIDs.value=['project-chapter']
 await f.state.proposeAction();const oldConfirmation=f.state.confirming.value
 assert.ok(oldConfirmation)
 f.props.project={...f.props.project,id:'second-project'};await tick()
 assert.deepEqual([...f.state.changeDraftChapterIDs.value],[])
 assert.equal(f.state.changeDraftTurn.value,null);assert.equal(f.state.actionForm.changeDraft,'')
 assert.equal(f.state.confirming.value,null)
 await assert.rejects(()=>oldConfirmation.execute('旧界面不得提交'),/课题或身份已变化/)
 assert.equal(f.calls.some(call=>call.method==='createActionProposal'),false)
 f.close()
})

test('模型合法章节仅预选，最终确认前不可见的章节阻断请求',async()=>{
 const f=fixture({workspace:{listChapters:project=>Promise.resolve({data:[changeDraftChapter(project)]})}});await tick()
 const turn=changeDraftTurn();turn.suggestions.change_draft.affected_chapter_ids=['chapter-1']
 f.state.prepareAgentChangeDraft(turn)
 assert.deepEqual([...f.state.changeDraftChapterIDs.value],['chapter-1'])
 assert.equal(JSON.parse(f.state.actionForm.changeDraft).affected_chapter_ids,undefined)
 assert.equal(f.calls.some(call=>call.method==='createActionProposal'),false)
 await f.state.proposeAction();assert.ok(f.state.confirming.value)
 f.state.chapters.value=[]
 f.state.confirmChecked.value=true;f.state.confirmReason.value='核对后当前章节已不可见'
 await f.state.executeConfirmation()
 assert.equal(f.calls.some(call=>call.method==='createActionProposal'),false)
 assert.match(f.state.error.value,/陈旧|不可见/)
 assert.equal(JSON.parse(f.state.actionForm.changeDraft).fields.topic.new_value,'模型拟议主题')
 f.close()
})

test('直接触发函数也拒绝其他项目章节及当前只读身份',async()=>{
 const f=fixture({workspace:{listChapters:project=>Promise.resolve({data:[changeDraftChapter(project),changeDraftChapter('other-project','other-chapter')]})}});await tick()
 f.state.prepareAgentChangeDraft(changeDraftTurn());f.state.changeDraftChapterIDs.value=['other-chapter']
 await f.state.proposeAction()
 assert.equal(f.calls.some(call=>call.method==='createActionProposal'),false)
 assert.match(f.state.error.value,/不属于当前课题/)
 f.state.changeDraftChapterIDs.value=['chapter-1'];f.props.readonly=true;await tick()
 await f.state.proposeAction()
 assert.equal(f.calls.some(call=>call.method==='createActionProposal'),false)
 assert.match(f.state.error.value,/无权/)
 f.close()
})

test('助手两组说明各自带对象、原因与依据，且不混用影响解析的 certainty',async()=>{
 const f=fixture();await tick()
 const turn=changeDraftTurn()
 turn.suggestions.affected_explanation={
  definite_affected:[{object:{kind:'ChapterVersion',id:'chapter-1',version:'v1'},reason:'该章正文引用了被修改的研究条件',basis:{source_type:'dependency_edge',source_id:'edge-1'}}],
  possible_affected:[{object:{kind:'EvidenceCard',id:'card-1',version:'2'},reason:'证据卡适用范围可能覆盖新条件',basis:{source_type:'ruleset',source_id:'ruleset-1'}}],
 }
 f.state.turns.value=[turn];await tick()
 const groups=f.affected.assistantAffectedGroups(turn.suggestions.affected_explanation)
 assert.equal(groups.length,2)
 assert.equal(groups[0].title,'确定受影响')
 assert.equal(groups[1].title,'可能受影响')
 assert.equal(f.affected.assistantAffectedObjectLabel(groups[0].entries[0]),'ChapterVersion · chapter-1 · v1')
 assert.equal(f.affected.assistantAffectedBasisLabel(groups[0].entries[0]),'dependency_edge · edge-1')
 assert.equal(JSON.stringify(groups).includes('certainty'),false)
 f.close()
})
