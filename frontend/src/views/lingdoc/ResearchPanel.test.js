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
test('权限拒绝清空本地任务草稿并给出可读错误，且不产生第二次写入',async()=>{
 const writes=[]
 const f=fixture({api:{saveResearchTask:(...args)=>{writes.push(args);return Promise.reject({status:403,message:'权限已变化'})}}});await tick()
 Object.assign(f.state.taskForm,{goal:'权限拒绝后被清空',queries:'定义\n反例',assets:['asset'],scope:'仅此资料'})
 await f.state.saveTask()
 assert.equal(writes.length,1)
 assert.equal(f.state.taskForm.goal,'')
 assert.match(f.state.error.value,/权限已变化/)
 await f.state.saveTask()
 assert.equal(writes.length,1)
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

function researchProposalTurn(){return {id:'proposal-turn',project_id:'project',session_id:'session',version:2,created_by:'owner',status:'completed',agent:'research_agent',reply:'我整理了一条检索计划。',run_mode:'real_api_fake_model',model_id:'synthetic-model',user_input:'帮我找合成材料的资料',context:{project_version:7,spec_revision:3,context_revision:4,source_refs:[]},suggestions:{task_card:{goal:'弄清合成材料的定义',question_type:'定义',scope:'只查项目资料',exclusions:['不含二手综述'],queries:['合成材料 定义','合成材料 反例','synthetic material definition'],expected_output:'候选资料与反例'}}}}

// 服务端拒掉提案时的那一轮：拒的是提案，不是整轮的问答复——这一轮照样完成，
// 答复照样在，只有那份建议被摘掉，并留下一个读得懂的失败码。
function refusedProposalTurn(){const base=researchProposalTurn();return {...base,status:'completed',failure_code:'invalid_proposal',suggestions:{reply:base.reply}}}

test('服务端校验过的提案放进可编辑表单，同义词留在查询词里，保存前不创建任何东西',async()=>{
 const f=fixture({api:{saveResearchTask:(project,input)=>{f.calls.push({method:'saveResearchTask',args:[project,input]});return Promise.resolve({data:{...task(),goal:input.goal,question_type:input.question_type,scope:input.scope,exclusions:input.exclusions,queries:input.queries,expected_output:input.expected_output}})}}});await tick()
 const turn=researchProposalTurn();f.state.turns.value=[turn];await tick()
 f.state.adoptTaskSuggestion(turn);await tick()
 assert.equal(f.state.taskForm.goal,'弄清合成材料的定义')
 assert.equal(f.state.taskForm.queries,'合成材料 定义\n合成材料 反例\nsynthetic material definition')
 assert.equal(f.state.taskForm.type,'定义')
 assert.equal(f.state.taskForm.scope,'只查项目资料')
 assert.equal(f.state.taskForm.exclusions,'不含二手综述')
 assert.equal(f.state.taskForm.output,'候选资料与反例')
 assert.equal(f.calls.some(call=>call.method==='saveResearchTask'),false)
 f.state.taskForm.assets=['asset']
 await f.state.saveTask();await tick()
 const saved=f.calls.find(call=>call.method==='saveResearchTask')
 assert.ok(saved)
 assert.deepEqual([...saved.args[1].queries],['合成材料 定义','合成材料 反例','synthetic material definition'])
 assert.equal(saved.args[1].goal,'弄清合成材料的定义')
 assert.deepEqual([...saved.args[1].exclusions],['不含二手综述'])
 f.close()
})

test('被服务端拒绝的提案不进入表单，只留下读得懂的失败',async()=>{
 const f=fixture();await tick()
 const turn=refusedProposalTurn()
 f.state.turns.value=[turn];await tick()
 f.state.adoptTaskSuggestion(turn);await tick()
 assert.equal(f.state.taskForm.goal,'')
 assert.equal(f.state.taskForm.queries,'')
 assert.equal(f.calls.some(call=>call.method==='saveResearchTask'),false)
 assert.match(f.state.failureLabel('invalid_proposal'),/未通过服务端校验/)
 f.close()
})

// A custom host renderer: the actual compiled template is rendered, so the
// panel's own markup decides what the user sees. No browser, no network.
const host=Vue.createRenderer({
 createElement:type=>({type,tagName:type.toUpperCase(),children:[],props:{},listeners:{},parent:null,value:'',checked:false,multiple:false,selectedIndex:-1,get options(){return this.children.filter(n=>n.type==='option')},getRootNode(){return global.document},getAttribute(k){return this.props[k]},addEventListener(k,h){this.listeners[k]=h},removeEventListener(k){delete this.listeners[k]},showModal(){},close(){},querySelector(){return null},focus(){}}),
 createText:text=>({type:'#text',text,children:[],parent:null}),createComment:text=>({type:'#comment',text,children:[],parent:null}),
 setElementText(n,t){n.text=t},setText(n,t){n.text=t},patchProp(n,k,_old,v){n.props[k]=v;if(['value','checked','selected','multiple','disabled','type'].includes(k))n[k]=v;if(k==='value')n._value=v},
 insert(n,p,a=null){if(n.parent)n.parent.children.splice(n.parent.children.indexOf(n),1);const at=a?p.children.indexOf(a):-1;p.children.splice(at<0?p.children.length:at,0,n);n.parent=p},
 remove(n){if(n.parent)n.parent.children.splice(n.parent.children.indexOf(n),1)},parentNode:n=>n.parent,
 nextSibling:n=>n.parent?.children[n.parent.children.indexOf(n)+1]||null,
})
const nodeText=n=>n.type==='#comment'?'':(n.text||'')+(n.children||[]).map(nodeText).join('')
const allNodes=n=>[n,...(n.children||[]).flatMap(allNodes)]
const byClass=(root,name)=>allNodes(root).filter(n=>n.props&&n.props.class===name)

function renderedFixture(options={}){
 const calls=[],cleanup=[],emitted=[],project={id:'project',name:'合成测试',status:'active',project_version:7,spec_revision:3,current_context_revision:4,spec:{topic:'合成主题'},template_id:'template',template_version:'1',members:[{user_id:'owner',role:'owner'}]}
 const props=Vue.reactive({project,readonly:false,currentUserId:'owner',...options.props})
 const defaultAPI=new Proxy({}, {get:(_,method)=>(...args)=>{calls.push({method,args});if(method==='listReleases')return Promise.resolve({data:{items:[]}});return Promise.resolve({data:[]})}})
 const g2=new Proxy(options.api||{}, {get:(target,method)=>method in target?target[method]:defaultAPI[method]})
 const workspace=new Proxy(options.workspace||{}, {get:(target,method)=>method in target?target[method]:defaultAPI[method]})
 global.document={activeElement:null};global.HTMLElement=class {};global.Document=class {};global.ShadowRoot=class {}
 const raw=fs.readFileSync(new URL('./ResearchPanel.vue',import.meta.url),'utf8'),parsed=parse(raw)
 assert.equal(parsed.errors.length,0)
 const script=compileScript(parsed.descriptor,{id:'g2-render-test'})
 const template=compileTemplate({source:parsed.descriptor.template.content,filename:'ResearchPanel.vue',id:'g2-render-test',compilerOptions:{bindingMetadata:script.bindings}})
 assert.equal(template.errors.length,0)
 const helperSource=fs.readFileSync(new URL('./researchUiState.ts',import.meta.url),'utf8'),helpers={}
 vm.runInNewContext(ts.transpileModule(helperSource,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText,{exports:helpers,URL,crypto:{randomUUID:()=>String(calls.length)}})
 const affectedSource=fs.readFileSync(new URL('./assistantAffectedGroups.ts',import.meta.url),'utf8'),affected={}
 vm.runInNewContext(ts.transpileModule(affectedSource,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText,{exports:affected,URL})
 const require_=id=>{
  if(id==='vue')return {...Vue,onBeforeUnmount:callback=>cleanup.push(callback)}
  if(id==='@/stores/auth')return {useAuthStore:()=>({currentUserId:props.currentUserId,effectiveTenantId:'fixture-tenant'})}
  if(id==='@/api/lingdoc/permissions')return {getEffectivePermission:()=>Promise.resolve({data:{allowed:false,capability:'manage',project_version:props.project.project_version,reason_code:'fixture-denied'}})}
  if(id==='@/api/lingdoc/agentApi')return g2
  if(id==='@/api/lingdoc/workspace')return workspace
  if(id==='./researchUiState')return helpers
  if(id==='./assistantAffectedGroups')return affected
  if(['@/api/lingdoc/delivery','@/api/webSearchProvider','@/api/knowledge-base'].includes(id))return defaultAPI
  throw new Error('unexpected dependency '+id)
 }
 const scriptBox={exports:{}}
 vm.runInNewContext(ts.transpileModule(script.content,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText,{exports:scriptBox.exports,console,document:{activeElement:null},setTimeout,clearTimeout,require:require_})
 const renderBox={exports:{}}
 vm.runInNewContext(ts.transpileModule(template.code,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText,{exports:renderBox.exports,console,document:{activeElement:null},setTimeout,clearTimeout,require:require_})
 const component=scriptBox.exports.default
 let state
 const wrapped={...component,render:renderBox.exports.render,setup(p,ctx){state=component.setup(p,{expose:()=>{},emit:(...args)=>emitted.push(args)});return state}}
 const container={children:[]}
 const app=host.createApp({render:()=>Vue.h(wrapped,{...props,onChanged:()=>{},onDirty:()=>{}})})
 app.mount(container)
 return {state,props,calls,emitted,container,ui:()=>nodeText(container),close:()=>app.unmount()}
}

test('提案的候选来源与各自理由在面板上分列展示，来源与理由不是一段话',async()=>{
 const f=renderedFixture();await tick()
 const base=researchProposalTurn()
 const turn={...base,suggestions:{...base.suggestions,task_card:{...base.suggestions.task_card,candidate_sources:[{source_id:'source-1',reason:'它写明了合成材料的定义'},{source_id:'source-2',reason:'它给出了反例'}]}}}
 f.state.tab.value='agents'
 f.state.turns.value=[turn]
 await tick()
 const ids=byClass(f.container,'proposal-source-id').map(nodeText)
 const reasons=byClass(f.container,'proposal-source-reason').map(nodeText)
 assert.deepEqual(ids,['source-1','source-2'])
 assert.deepEqual(reasons,['它写明了合成材料的定义','它给出了反例'])
 // 分列：来源与理由各自在自己的格子里，没有被揉成一段话
 for(const node of byClass(f.container,'proposal-source-id'))assert.doesNotMatch(nodeText(node),/它写明了|它给出了/)
 for(const node of byClass(f.container,'proposal-source-reason'))assert.doesNotMatch(nodeText(node),/source-\d/)
 assert.match(f.ui(),/候选来源/)
 f.close()
})

test('被服务端拒绝的提案在面板上不出现任何候选来源',async()=>{
 const f=renderedFixture();await tick()
 const refused=refusedProposalTurn()
 f.state.tab.value='agents'
 f.state.turns.value=[refused]
 await tick()
 assert.equal(byClass(f.container,'proposal-source-id').length,0)
 assert.equal(byClass(f.container,'proposal-source-reason').length,0)
 assert.doesNotMatch(f.ui(),/source-1/)
 f.close()
})

// A13: 理由与缺口在提案块里分列展示：理由一条一行，缺口分「缺什么」与
// 「期望补什么」两列，都不与候选来源揉成一段话。
test('提案的理由与缺口在面板上分列展示，缺口分缺什么与期望补什么',async()=>{
 const f=renderedFixture();await tick()
 const base=researchProposalTurn()
 const turn={...base,suggestions:{...base.suggestions,task_card:{...base.suggestions.task_card,reasons:['目标定成「定义」是因为用户问的就是这个词本身','范围只划到项目资料是因为公开综述尚未授权'],gaps:[{description:'缺一篇给出反例的对照研究',expected_evidence:'一篇标注了反例的同行评议文章'}]}}}
 f.state.tab.value='agents'
 f.state.turns.value=[turn]
 await tick()
 const reasons=byClass(f.container,'proposal-reason').map(nodeText)
 assert.deepEqual(reasons,['目标定成「定义」是因为用户问的就是这个词本身','范围只划到项目资料是因为公开综述尚未授权'])
 const missing=byClass(f.container,'proposal-gap-description').map(nodeText)
 const expected=byClass(f.container,'proposal-gap-evidence').map(nodeText)
 assert.deepEqual(missing,['缺一篇给出反例的对照研究'])
 assert.deepEqual(expected,['一篇标注了反例的同行评议文章'])
 // 分列：理由、缺什么、补什么各自在自己的格子里，没有被揉成一段话
 for(const node of byClass(f.container,'proposal-reason'))assert.doesNotMatch(nodeText(node),/缺一篇给出反例|一篇标注了反例/)
 for(const node of byClass(f.container,'proposal-gap-description'))assert.doesNotMatch(nodeText(node),/一篇标注了反例/)
 for(const node of byClass(f.container,'proposal-gap-evidence'))assert.doesNotMatch(nodeText(node),/缺一篇给出反例/)
 assert.match(f.ui(),/建议理由/)
 assert.match(f.ui(),/还缺什么/)
 f.close()
})

// A13: 服务端拒掉的提案，理由与缺口都不到界面上。
test('被服务端拒绝的提案在面板上不出现理由与缺口',async()=>{
 const f=renderedFixture();await tick()
 const refused=refusedProposalTurn()
 f.state.tab.value='agents'
 f.state.turns.value=[refused]
 await tick()
 assert.equal(byClass(f.container,'proposal-reason').length,0)
 assert.equal(byClass(f.container,'proposal-gap-description').length,0)
 assert.equal(byClass(f.container,'proposal-gap-evidence').length,0)
 assert.doesNotMatch(f.ui(),/缺一篇给出反例/)
 f.close()
})

// A5/A6: 坏建议不连累用户那一轮的问答复。这一轮照样完成、答复照样在，
// 面板上读得到「本轮的建议未通过服务端校验」，但看不到那份建议，也看不到
// 它的候选来源、理由与缺口。
test('坏提案不连累那一轮的问答复：答复仍在，面板上没有建议',async()=>{
 const f=renderedFixture();await tick()
 const refused=refusedProposalTurn()
 f.state.tab.value='agents'
 f.state.turns.value=[refused]
 await tick()
 assert.match(f.ui(),/我整理了一条检索计划。/)
 assert.match(f.ui(),/未通过服务端校验/)
 assert.equal(byClass(f.container,'research-proposal').length,0)
 assert.equal(byClass(f.container,'proposal-source-id').length,0)
 assert.equal(byClass(f.container,'proposal-reason').length,0)
 f.close()
})

// A5: 没有提案的那一轮照样有答复，这一轮没有提案，也不算失败。
test('没有提案的那一轮照样有答复，面板上没有建议',async()=>{
 const f=renderedFixture();await tick()
 const base=researchProposalTurn()
 const absent={...base,suggestions:{reply:base.reply}}
 f.state.tab.value='agents'
 f.state.turns.value=[absent]
 await tick()
 assert.match(f.ui(),/我整理了一条检索计划。/)
 assert.doesNotMatch(f.ui(),/未通过服务端校验/)
 assert.equal(byClass(f.container,'research-proposal').length,0)
 f.close()
})

// A2: 助手那张同名卡片没有经过提案契约的校验，界面不得把它标成「服务端
// 校验过的研究提案」，也不得宣称候选来源「服务端已逐条核对本轮授权」——
// 那是替服务端宣称一件没有发生过的事，助手编一个来源 id 就会被界面背书。
test('助手轮次的卡片不被标成服务端校验过的研究提案',async()=>{
 const f=renderedFixture();await tick()
 const base=researchProposalTurn()
 const assistantTurn={...base,agent:'task_assistant',suggestions:{...base.suggestions,task_card:{...base.suggestions.task_card,candidate_sources:[{source_id:'source-1',reason:'它写明了合成材料的定义'}]}}}
 f.state.tab.value='agents'
 f.state.turns.value=[assistantTurn]
 await tick()
 assert.doesNotMatch(f.ui(),/服务端校验过的研究提案/)
 assert.doesNotMatch(f.ui(),/服务端已逐条核对本轮授权/)
 assert.equal(byClass(f.container,'research-proposal').length,0)
 assert.equal(byClass(f.container,'proposal-source-id').length,0)
 f.close()
})

// A2 的另一半：调研智能体那一轮的提案仍标着「服务端校验过的」——它确实
// 经过提案契约的校验。
test('调研智能体轮次的提案仍标为服务端校验过的',async()=>{
 const f=renderedFixture();await tick()
 f.state.tab.value='agents'
 f.state.turns.value=[researchProposalTurn()]
 await tick()
 assert.match(f.ui(),/服务端校验过的研究提案/)
 assert.equal(byClass(f.container,'research-proposal').length,1)
 f.close()
})

// N5: 失败码有自己的词表。'outcome_unknown' 出现在状态词表里（那是回执上
// 的状态）：拿状态词表当失败码词表，会把「原样显示代码」变成「结果待核对」。
// 失败码词表里没有的码原样显示；'invalid_proposal' 仍有一句读得懂的话。
test('未登记的失败码原样显示，不被状态词表翻译',async()=>{
  const f=renderedFixture();await tick()
  const base=researchProposalTurn()
  f.state.tab.value='agents'
  f.state.turns.value=[{...base,failure_code:'outcome_unknown'}]
  await tick()
  assert.match(f.ui(),/outcome_unknown/)
  assert.doesNotMatch(f.ui(),/结果待核对/)
  assert.equal(f.state.failureLabel('outcome_unknown'),'outcome_unknown')
  assert.match(f.state.failureLabel('invalid_proposal'),/未通过服务端校验/)
  f.close()
})

// A14: 助手轮次那张同名卡片没有经过提案契约的校验，面板因此不把它显示成
// 「服务端校验过的研究提案」——但采纳路径对助手轮次仍然开着，这是 #401
// 之前就有的行为，本票不收它。这条用例守住它：采纳按钮仍在，采纳后那份
// 建议进了可编辑表单（缺的那些字段就是缺，不渲染出 undefined），而调研
// 任务尚未创建——采纳只把建议放进草稿，保存才建任务。
test('助手轮次的卡片仍可被采纳：按钮在、表单被填好、调研任务尚未创建',async()=>{
  const f=renderedFixture();await tick()
  const base=researchProposalTurn()
  const assistantTurn={...base,agent:'task_assistant',suggestions:{...base.suggestions,task_card:{goal:'弄清合成材料的定义',queries:['合成材料 定义'],scope:'只查项目资料'}}}
  f.state.tab.value='agents'
  f.state.turns.value=[assistantTurn]
  await tick()
  // 提案块仍不渲染给助手轮次，但采纳按钮在
  assert.equal(byClass(f.container,'research-proposal').length,0)
  const adopt=allNodes(f.container).find(node=>node.type==='button'&&nodeText(node).trim()==='放入可编辑任务草稿')
  assert.ok(adopt,'助手轮次仍然有采纳按钮')
  await adopt.props.onClick({type:'click',preventDefault(){},stopPropagation(){}})
  await tick()
  // 那一轮的建议进了可编辑表单；它没写的字段就是空，不渲染出 undefined
  assert.equal(f.state.taskForm.goal,'弄清合成材料的定义')
  assert.equal(f.state.taskForm.queries,'合成材料 定义')
  assert.equal(f.state.taskForm.scope,'只查项目资料')
  assert.equal(f.state.taskForm.type,'')
  assert.equal(f.state.taskForm.exclusions,'')
  assert.equal(f.state.taskForm.output,'')
  assert.doesNotMatch(f.ui(),/undefined/)
  // 采纳只填草稿：调研任务尚未创建
  assert.equal(f.calls.some(call=>call.method==='saveResearchTask'),false)
  f.close()
})

test('书目关联仅在人工确认后发送用户看到的资料版本与摘录哈希',async()=>{
 const sent=[]
 const f=fixture({api:{confirmAcademicSourceAssociation:(...args)=>{sent.push(args);return Promise.resolve({data:{id:'association-history'}})}}});await tick()
 f.state.imports.value=[{id:'candidate',title:'合成论文',doi:'10.5555/test',version:3,status:'imported',selection_type:'academic_metadata'}]
 f.state.source.value={id:'source',asset_id:'asset',asset_revision:2,locator:'第 1 段',quoted_text:'合成原文',quoted_text_hash:'viewed-hash',status:'available'}
 f.state.associationCandidate.value='candidate'
 f.state.confirmAcademicAssociation();await tick()
 assert.equal(sent.length,0)
 // A later UI update must not silently turn the reviewed version into a new assertion.
 f.state.source.value.asset_revision=3;f.state.source.value.quoted_text_hash='new-hash'
 await f.state.executeConfirmation();assert.equal(sent.length,0)
 f.state.confirmChecked.value=true;f.state.confirmReason.value='已查看上传原文并核对标题与 DOI'
 await f.state.executeConfirmation()
 assert.equal(sent.length,1);assert.equal(sent[0][1].expected_version,3);assert.equal(sent[0][1].expected_asset_revision,2);assert.equal(sent[0][1].expected_quoted_text_hash,'viewed-hash');assert.equal(sent[0][1].expected_locator,'第 1 段')
 f.close()
})

test('撤销关联要求人工确认并携带当前记录版本',async()=>{
 const sent=[]
 const f=fixture({api:{revokeAcademicSourceAssociation:(...args)=>{sent.push(args);return Promise.resolve({data:{id:'association',version:2,status:'revoked'}})}}});await tick()
 f.state.revokeAcademicAssociation({id:'association',version:1,status:'recorded',review_state:'historical'});await tick()
 await f.state.executeConfirmation();assert.equal(sent.length,0)
 f.state.confirmChecked.value=true;f.state.confirmReason.value='撤销之前的人工判断';await f.state.executeConfirmation()
 assert.equal(sent.length,1);assert.equal(sent[0][1],'association');assert.equal(sent[0][2].expected_version,1);assert.equal(sent[0][2].confirmed,true)
 f.close()
})

test('候选卡固定关联判断版本，关联变化时不能悄悄换版',async()=>{
 const f=fixture();await tick()
 f.state.anchors.value=[{id:'anchor',version:2,status:'CURRENT',source_id:'source',asset_id:'asset',asset_revision:1,locator:'第一页'}]
 f.state.associationHistory.value=[{id:'assoc',version:3,status:'recorded',review_state:'historical',source_id:'source',asset_id:'asset',asset_revision:1,doi:'10.5555/synthetic',locator:'第一页'}]
 Object.assign(f.state.cardForm,{anchors:['anchor'],academicAssociations:[{id:'assoc',version:3}],chapters:['chapter'],scope:'synthetic scope'})
 const input=f.state.cardInput();assert.equal(input.academic_association_refs[0].id,'assoc');assert.equal(input.academic_association_refs[0].version,3)
 f.state.associationHistory.value[0].version=4
 assert.throws(()=>f.state.cardInput(),/书目判断已变化/)
 f.state.confirmCard({academic_association_refs:[{id:'assoc',version:3}]});assert.match(f.state.error.value,/暂不能确认或用于写作/)
 f.close()
})
