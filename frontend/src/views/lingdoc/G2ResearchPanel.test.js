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
 const raw=fs.readFileSync(new URL('./G2ResearchPanel.vue',import.meta.url),'utf8'),parsed=parse(raw)
 assert.equal(parsed.errors.length,0)
 const script=compileScript(parsed.descriptor,{id:'g2-test'})
 const template=compileTemplate({source:parsed.descriptor.template.content,filename:'G2ResearchPanel.vue',id:'g2-test',compilerOptions:{bindingMetadata:script.bindings}})
 assert.equal(template.errors.length,0)
 const exports={}
 const helperSource=fs.readFileSync(new URL('./g2UiState.ts',import.meta.url),'utf8'),helpers={}
 vm.runInNewContext(ts.transpileModule(helperSource,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText,{exports:helpers,URL,crypto:{randomUUID:()=>String(calls.length)}})
 const output=ts.transpileModule(script.content,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
 const emitted=[]
 vm.runInNewContext(output,{exports,console,document:{activeElement:null},setTimeout,clearTimeout,require:id=>{
  if(id==='vue')return {...Vue,onBeforeUnmount:callback=>cleanup.push(callback)}
  if(id==='@/stores/auth')return {useAuthStore:()=>({currentUserId:props.currentUserId,effectiveTenantId:'fixture-tenant'})}
  if(id==='@/api/lingdoc/permissions')return {getEffectivePermission:()=>Promise.resolve({data:{allowed:false,capability:'manage',project_version:props.project.project_version,reason_code:'fixture-denied'}})}
  if(id==='@/api/lingdoc/g2')return g2
  if(id==='@/api/lingdoc/workspace')return workspace
  if(id==='./g2UiState')return helpers
  if(['@/api/lingdoc/delivery','@/api/web-search-provider','@/api/knowledge-base'].includes(id))return defaultAPI
  throw new Error('unexpected dependency '+id)
 }})
 const state=exports.default.setup(props,{expose:()=>{},emit:(...args)=>emitted.push(args)})
 return {state,props,calls,emitted,close:()=>cleanup.forEach(fn=>fn())}
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
