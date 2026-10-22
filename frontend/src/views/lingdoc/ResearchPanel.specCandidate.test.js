import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
const require = createRequire(import.meta.url)
const __filename = fileURLToPath(import.meta.url)
const __dirname = require('node:path').dirname(__filename)

// Actual SFC setup, mounted Vue watchers and compiled template; typed HTTP modules
// run unchanged. DOM host and server responses are synthetic fixtures, with no
// browser paint, network, provider call or assertion of scientific correctness.
const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),crypto=require('node:crypto')
const Vue=require('vue'),ts=require('typescript'),SFC=require('@vue/compiler-sfc')
const frontend=path.resolve(__dirname,'../../..')
const clone=x=>JSON.parse(JSON.stringify(x)),response=data=>({data,meta:{replayed:false,refresh_required:false},request_id:'synthetic-g1-source'})
const tick=async()=>{for(let i=0;i<3;i++){await Vue.nextTick();await new Promise(resolve=>setImmediate(resolve))}}
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return {promise,resolve,reject}}
function project(version=7,spec=3){return {id:'p',name:'合成条件项目',status:'draft',project_version:version,spec_revision:spec,current_context_revision:4,
 spec:{topic:'已保存主题',goal:'必须保留的其他字段',orphaned:'孤立字段也保留'},template_id:'template',template_version:'1',template_copy_version:1,
 template_copy:{id:'copy-p',version:1,content_hash:'actual-copy-hash',definition:{id:'template',version:'1',fields:[{field_id:'topic',label:'主题',type:'string',required:true},{field_id:'goal',label:'目标',type:'string',required:true}]}},
 members:[{user_id:'u',role:'owner',status:'active'}]}}
function turn(){return {id:'actual-turn',project_id:'p',session_id:'actual-session',version:2,agent:'task_assistant',created_by:'u',user_input:'合成问题',reply:'原样合成会话回复',
 status:'completed',run_mode:'mock',context:{project_id:'p',project_version:7,spec_revision:3,context_revision:4,source_refs:[{source_id:'server-source',asset_id:'asset',asset_revision:2,locator:'服务端实际定位',quoted_text_hash:'server-hash',model_use_allowed:true}],card_refs:[],output_constraints:[],authorization_state:'allowed'}}}
function session(){return {id:'actual-session',project_id:'p',version:1,agent:'task_assistant',created_by:'u',created_at:'2026-10-14T00:00:00Z'}}
const host=Vue.createRenderer({
 createElement:type=>({type,tagName:type.toUpperCase(),children:[],props:{},listeners:{},parent:null,value:'',checked:false,multiple:false,selectedIndex:-1,get options(){return this.children.filter(n=>n.type==='option')},getRootNode(){return global.document},getAttribute(k){return this.props[k]},addEventListener(k,h){this.listeners[k]=h},removeEventListener(k){delete this.listeners[k]},showModal(){},close(){},querySelector(){return null},focus(){}}),
 createText:text=>({type:'#text',text,children:[],parent:null}),createComment:text=>({type:'#comment',text,children:[],parent:null}),
 setElementText(n,t){n.text=t},setText(n,t){n.text=t},patchProp(n,k,_old,v){n.props[k]=v;if(['value','checked','selected','multiple','disabled','type'].includes(k))n[k]=v;if(k==='value')n._value=v},
 insert(n,p,a=null){if(n.parent)n.parent.children.splice(n.parent.children.indexOf(n),1);const at=a?p.children.indexOf(a):-1;p.children.splice(at<0?p.children.length:at,0,n);n.parent=p},
 remove(n){if(n.parent)n.parent.children.splice(n.parent.children.indexOf(n),1)},parentNode:n=>n.parent,
 nextSibling:n=>n.parent?.children[n.parent.children.indexOf(n)+1]||null,
})
const text=n=>n.type==='#comment'?'':(n.text||'')+(n.children||[]).map(text).join('')
function nodes(n){return [n,...(n.children||[]).flatMap(nodes)]}
const actual=fs.readFileSync(path.join(__dirname,'ResearchPanel.vue'),'utf8'),parsed=SFC.parse(actual,{filename:'ResearchPanel.vue'})
assert.deepEqual(parsed.errors,[])
const script=SFC.compileScript(parsed.descriptor,{id:'g1-spec-source-test'}),template=SFC.compileTemplate({source:parsed.descriptor.template.content,filename:'ResearchPanel.vue',id:'g1-spec-source-test',compilerOptions:{bindingMetadata:script.bindings}})
assert.deepEqual(template.errors,[])
const compile=s=>ts.transpileModule(s,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022,esModuleInterop:true}}).outputText
function fixture(options={}){
 const calls=[],events=[],cache=new Map(),auth=Vue.reactive({currentUserId:'u',effectiveTenantId:1})
 const props=Vue.reactive({project:project(),readonly:false,currentUserId:'u',selectedEvidence:[],...options.props})
 const f={calls,events,auth,props,currentProject:clone(props.project),permission:options.permission||'allow',permissionRead:options.permissionRead,put:options.put,sourceTurn:turn(),sourceSession:session()}
 global.document={activeElement:null};global.HTMLElement=class {};global.Document=class {};global.ShadowRoot=class {}
 const request={get:async(url,config)=>{
  calls.push({method:'GET',url,config})
  if(url.endsWith('/permissions')){
   if(f.permissionRead)return f.permissionRead(url,config)
   if(f.permission==='reject')throw {status:403,message:'管理许可撤回'}
   return response({allowed:f.permission==='allow',capability:'manage',project_version:f.currentProject.project_version,context_revision:f.currentProject.current_context_revision})
  }
  if(url==='/api/v1/lingdoc/projects/p')return response(clone(f.currentProject))
  if(url.includes('/agent-sessions/'))return response([clone(f.sourceTurn)])
  if(url.endsWith('/agents/task_assistant/sessions'))return response([clone(f.sourceSession)])
  return response([])
 },put:async(url,body,config)=>{
  const call={method:'PUT',url,body:clone(body),key:config?.headers?.['Idempotency-Key']};calls.push(call)
  if(f.put)return f.put(call)
  const field=Object.keys(body.field_metadata)[0],meta=body.field_metadata[field]
  return response({...clone(f.currentProject),spec_revision:body.expected_spec_revision+1,project_version:f.currentProject.project_version+1,spec:clone(body.fields),
   spec_fields:{[field]:{value:body.fields[field],...clone(meta),modified_by:'u',modified_at:'2026-10-14T00:00:00Z'}}})
 },post:async(url,body,config)=>{calls.push({method:'POST',url,body:clone(body),config});return response({})}}
 const ports=new Proxy({}, {get:(_,name)=>(...args)=>{calls.push({method:String(name),args});return Promise.resolve(response(name==='listReleases'||name==='listKnowledgeBases'?{items:[]}:[]))}})
 function evaluate(source,file){
  const box={exports:{}}
  const load=id=>{
   if(id==='vue')return Vue
   if(id==='@/stores/auth')return {useAuthStore:()=>auth}
   if(id==='@/utils/request')return request
   if(['@/api/lingdoc/agentApi','@/api/lingdoc/workspace','@/api/lingdoc/permissions'].includes(id))return local(path.join(frontend,'src',id.slice(2)+'.ts'))
   if(id.startsWith('@/api/'))return ports
   if(id==='./researchUiState')return local(path.join(__dirname,'researchUiState.ts'))
   return require(id)
  }
  new Function('require','module','exports',compile(source))(load,box,box.exports);return box.exports
 }
 function local(file){if(!cache.has(file))cache.set(file,evaluate(fs.readFileSync(file,'utf8'),file));return cache.get(file)}
 const component=evaluate(script.content,'ResearchPanel.vue').default
 const rendered=evaluate(template.code,'ResearchPanel.render.js').render
 let state
 const wrapped={...component,render:rendered,setup(p,ctx){state=component.setup(p,ctx);return state}}
 const container={children:[]},app=host.createApp({render:()=>Vue.h(wrapped,{...props,onChanged:()=>events.push(['changed']),onDirty:value=>events.push(['dirty',value])})})
 app.mount(container)
 Object.assign(f,{state,container,buttons:label=>nodes(container).filter(n=>n.type==='button'&&text(n).trim()===label),ui:()=>text(container),
  writes:()=>calls.filter(c=>c.method==='PUT'),close:()=>app.unmount(),
  async open(){await tick();state.tab.value='agents';state.sessionId.value='actual-session';await tick();assert.equal(state.canUseSpecCandidate.value,options.permission!=='deny'&&options.permission!=='reject');return f},
  async prepare(value='原样合成会话回复'){state.prepareSpecCandidate(state.turns.value[0]);await tick();assert.ok(state.specCandidate.value);state.specCandidate.value.field='topic';state.specCandidate.value.value=value;await tick()},
  async confirm(){state.confirmSpecCandidate();await tick();if(!state.confirming.value)return;state.confirmChecked.value=true;state.confirmReason.value='合成界面人工核对';await state.executeConfirmation();await tick()},
 })
 return f
}
test('实际完成轮次入口需要当前草稿管理许可，失败/未知/他人/隐藏 legacy 均不显示',async()=>{
 for(const invalid of [{status:'failed'},{status:'outcome_unknown'},{reply:''},{version:0},{project_id:'other'},{created_by:'other'}]){
  const f=fixture();try{Object.assign(f.sourceTurn,invalid);await f.open();assert.equal(f.buttons('用于研究条件候选').length,0);f.state.prepareSpecCandidate(f.state.turns.value[0]);assert.equal(f.state.specCandidate.value,null)}finally{f.close()}
 }
 for(const permission of ['deny','reject']){
  const f=fixture({permission});try{await f.open();assert.equal(f.buttons('用于研究条件候选').length,0);f.state.prepareSpecCandidate(f.state.turns.value[0]);assert.equal(f.state.specCandidate.value,null)}finally{f.close()}
 }
 const f=fixture();try{await f.open();assert.equal(f.buttons('用于研究条件候选').length,1);assert.equal(f.writes().length,0)}finally{f.close()}
})
test('原样/编辑值只在明确确认后保存真实 agent_turn，merge 所有字段且不调用模型或自动立项',async()=>{
 for(const [value,origin] of [['原样合成会话回复','ai_generated'],['人工从回复整理的新值','ai_assisted_human']]){
  const f=fixture();try{
   await f.open();await f.prepare(value);f.state.confirmSpecCandidate();await tick()
   assert.equal(f.writes().length,0);await f.state.executeConfirmation();assert.equal(f.writes().length,0)
   f.state.confirmChecked.value=true;f.state.confirmReason.value='人工核对，仅合成检查';await f.state.executeConfirmation();await tick()
   const [write]=f.writes();assert.equal(f.writes().length,1);assert.equal(write.url,'/api/v1/lingdoc/projects/p/spec')
   assert.deepEqual(write.body.fields,{topic:value,goal:'必须保留的其他字段',orphaned:'孤立字段也保留'})
   assert.deepEqual(write.body.field_metadata,{topic:{origin,status:'pending_confirmation',provenance:{source_type:'agent_turn',source_id:'actual-turn',source_version:'2',purpose:'project_spec',locator:'reply'}}})
   assert.equal(write.body.expected_spec_revision,3);assert.equal('expected_project_version' in write.body,false)
   assert.equal(f.state.specCandidate.value,null);assert.match(f.ui(),/待人工确认/)
   assert.ok(!f.calls.some(c=>c.method==='POST'||c.url?.includes('/generations')),'no model, activation, read-receipt or extra action write')
  }finally{f.close()}
 }
})
test('同项目/UID/tenant 对象刷新保留输入；基线变化拒绝旧 CAS，实际重读及人工关联后才新 CAS',async()=>{
 const f=fixture();try{
  await f.open();await f.prepare('保留的候选输入');const basis=f.state.specCandidate.value.basis
  f.props.project=clone(f.props.project);await tick();assert.equal(f.state.specCandidate.value.value,'保留的候选输入');assert.equal(f.state.specCandidateStale.value,false)
  f.props.project={...clone(f.props.project),project_version:8,spec_revision:4,spec:{...f.props.project.spec,goal:'协作者新值'}}
  f.currentProject=clone(f.props.project);await tick()
  assert.equal(f.state.specCandidate.value.basis.specRevision,3);assert.equal(f.state.specCandidateStale.value,true)
  await f.confirm();assert.equal(f.writes().length,0)
  f.state.reassociateSpecCandidate();assert.equal(f.state.specCandidate.value.basis.specRevision,3,'cannot bind without actual read')
  await f.state.readSpecCandidateBasis();await tick();assert.equal(f.state.specCandidate.value.basis.specRevision,3,'read does not auto bind')
  f.state.reassociateSpecCandidate();await tick()
  assert.equal(f.state.specCandidate.value.value,'保留的候选输入');assert.equal(f.state.specCandidate.value.basis.specRevision,4)
  await f.confirm();assert.equal(f.writes().length,1);assert.equal(f.writes()[0].body.expected_spec_revision,4);assert.equal(f.writes()[0].body.fields.goal,'协作者新值')
 }finally{f.close()}
})
test('409 不重试、不伪装成功，保留候选、旧版本和原键',async()=>{
 const f=fixture({put:()=>Promise.reject({status:409,message:'Spec 版本冲突'})});try{
  await f.open();await f.prepare('不能丢的输入');await f.confirm()
  assert.equal(f.writes().length,1);assert.equal(f.state.specCandidate.value.value,'不能丢的输入');assert.equal(f.state.specCandidate.value.basis.specRevision,3);assert.equal(f.state.specCandidateUnknown.value,false)
  assert.match(f.ui(),/Spec 版本冲突/);await f.confirm()
  assert.equal(f.writes().length,2);assert.equal(f.writes()[0].key,f.writes()[1].key)
 }finally{f.close()}
})
test('未知回执保留原输入/精确请求；实际改版本后人工重试仍只用原 CAS/原字段/原键',async()=>{
 const f=fixture({put:()=>Promise.reject({status:504,message:'回执未确定'})});try{
  await f.open();await f.prepare('原未知请求');await f.confirm()
  assert.equal(f.writes().length,1);assert.equal(f.state.specCandidateUnknown.value,true)
  const original=clone(f.writes()[0]);assert.match(f.ui(),/不会自动再次保存/)
  f.props.project={...clone(f.props.project),project_version:8,spec_revision:4,spec:{...f.props.project.spec,goal:'新的服务端目标'}};f.currentProject=clone(f.props.project);await tick()
  assert.equal(f.writes().length,1);await f.state.readSpecCandidateBasis();assert.equal(f.state.specCandidateFresh.value,null)
  await f.confirm();assert.equal(f.writes().length,2);assert.deepEqual(f.writes()[1],original);assert.equal(f.state.specCandidate.value.value,'原未知请求')
 }finally{f.close()}
})
test('不完整成功 DTO 或其他 turn 回执不能清输入，也不能宣称保存成功',async()=>{
 for(const changed of [{spec_fields:undefined},{spec_revision:2},{id:'other'}]){
  const f=fixture({put:call=>response({...project(8,4),spec:call.body.fields,spec_fields:{topic:{value:call.body.fields.topic,...call.body.field_metadata.topic}},...changed})})
  try{await f.open();await f.prepare();await f.confirm();assert.equal(f.state.specCandidateUnknown.value,true);assert.ok(f.state.specCandidate.value);assert.ok(!f.events.some(e=>e[0]==='changed'));assert.match(f.ui(),/完整回执/)}finally{f.close()}
 }
})
test('确认期间字段或基线变更，旧确认不能写入',async()=>{
 const f=fixture();try{await f.open();await f.prepare('旧确认值');f.state.confirmSpecCandidate();await tick();f.state.specCandidate.value.value='确认后又改';f.state.confirmChecked.value=true;f.state.confirmReason.value='旧确认';await f.state.executeConfirmation();await tick();assert.equal(f.writes().length,0);assert.equal(f.state.specCandidate.value.value,'确认后又改')}finally{f.close()}
})
test('项目/调用者/tenant 切换使迟到保存、读取和权限响应全部失效',async()=>{
 for(const kind of ['project','user','tenant']){
  const pending=deferred(),f=fixture({put:()=>pending.promise})
  try{
   await f.open();await f.prepare('旧身份私有候选');f.state.confirmSpecCandidate();await tick();f.state.confirmChecked.value=true;f.state.confirmReason.value='人工';const saving=f.state.executeConfirmation();await tick()
   const original=clone(f.writes()[0])
   if(kind==='project')f.props.project={...clone(f.props.project),id:'new-project'}
   if(kind==='user'){f.props.currentUserId='another';f.auth.currentUserId='another'}
   if(kind==='tenant')f.auth.effectiveTenantId=2
   await tick();assert.equal(f.state.specCandidate.value,null)
   pending.resolve(response({...project(8,4),spec:original.body.fields,spec_fields:{topic:{value:original.body.fields.topic,...original.body.field_metadata.topic}}}));await saving;await tick()
   assert.equal(f.state.specCandidate.value,null);assert.ok(!f.events.some(e=>e[0]==='changed'))
  }finally{f.close()}
 }
 const pending=deferred(),f=fixture({permissionRead:()=>pending.promise})
 try{
  await tick();f.auth.effectiveTenantId=2;f.permissionRead=()=>response({allowed:false,capability:'manage',project_version:7});await tick()
  pending.resolve(response({allowed:true,capability:'manage',project_version:7}));await tick();assert.equal(f.state.canUseSpecCandidate.value,false)
 }finally{f.close()}
})
test('readonly 或当前来源 403 拒绝进一步保存，来源撤回时清除派生私有候选',async()=>{
 const f=fixture({put:()=>Promise.reject({status:403,message:'当前来源已撤回'})});try{
  await f.open();await f.prepare('派生私有文本');f.props.readonly=true;await tick();await f.confirm();assert.equal(f.writes().length,0);assert.equal(f.state.specCandidate.value.value,'派生私有文本')
  f.props.readonly=false;await tick();await f.confirm();assert.equal(f.writes().length,1);assert.equal(f.state.specCandidate.value,null);assert.match(f.ui(),/当前来源已撤回/)
 }finally{f.close()}
})


test('服务器已规范化的同值同版本 no-op 是有效回执，手填动作仍 human 且保留全字段',async()=>{
 const f=fixture();try{
  f.currentProject.spec.topic=f.sourceTurn.reply;f.props.project=clone(f.currentProject)
  f.put=call=>response({...clone(f.currentProject),spec:call.body.fields,spec_fields:{topic:{value:call.body.fields.topic,...call.body.field_metadata.topic}}})
  await f.open();await f.prepare();await f.confirm();assert.equal(f.writes().length,1);assert.equal(f.state.specCandidate.value,null)
  Object.assign(f.state.actionForm,{action:'save_spec',field:'topic',value:'人工手填动作',effect:'人工条件提案'})
  await f.state.proposeAction();await tick()
  const action=f.state.confirming.value;assert.ok(action);assert.match(action.summary,/必须保留的其他字段/)
  f.state.confirmChecked.value=true;f.state.confirmReason.value='人工';await f.state.executeConfirmation()
  const post=f.calls.find(c=>c.method==='POST'&&c.url.endsWith('/action-proposals'))
  assert.ok(post);assert.deepEqual(post.body.input.fields,{...f.props.project.spec,topic:'人工手填动作'});assert.equal(post.body.input.field_metadata,undefined)
 }finally{f.close()}
})
test('没有来源的真实 typed 完成轮次可整理纯会话候选，但模拟模式明确显示未调用真实模型',async()=>{
 const f=fixture();try{f.sourceTurn.context.source_refs=null;await f.open();await f.prepare();assert.match(f.ui(),/模拟回复，未调用真实模型/);assert.match(f.ui(),/本轮来源 0 份/);assert.equal(f.writes().length,0)}finally{f.close()}
})

test('已打开的派生候选在管理权限 GET 明确 403 撤回时停止显示，临时网络失败只锁保存',async()=>{
 for(const status of [403,503]){
  const f=fixture();try{await f.open();await f.prepare('已派生的私有会话内容');f.permissionRead=()=>Promise.reject({status,message:status===403?'成员授权已撤回':'暂时无法读取许可'});await f.state.readSpecManagePermission();await tick();assert.equal(f.state.canUseSpecCandidate.value,false);if(status===403){assert.equal(f.state.specCandidate.value,null);assert.doesNotMatch(f.ui(),/已派生的私有会话内容/)}else assert.equal(f.state.specCandidate.value.value,'已派生的私有会话内容');assert.equal(f.writes().length,0)}finally{f.close()}
 }
})
