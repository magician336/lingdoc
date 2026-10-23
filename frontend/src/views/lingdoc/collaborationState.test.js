import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
const require = createRequire(import.meta.url)
const __filename = fileURLToPath(import.meta.url)
const __dirname = require('node:path').dirname(__filename)

// Runs without downloads or a browser. It compiles the real Vue SFC in memory,
// invokes its actual handlers, and renders it with Vue's server renderer. The
// only external-service replacement is the explicitly synthetic HTTP fixture.
const test=require('node:test')
const assert=require('node:assert/strict')
const fs=require('node:fs')
const path=require('node:path')
const Module=require('node:module')
const frontend=path.resolve(__dirname,'../../..')
let deps
for(const directory of [frontend,path.resolve(__dirname,'../../../../../../repo/frontend')]){
 try {const load=Module.createRequire(path.join(directory,'package.json'));load('typescript');load('@vue/compiler-sfc');load('@vue/server-renderer');deps={load,directory};break}catch{}
}
if(!deps)throw new Error('Installed TypeScript/Vue SFC/server-renderer dependencies are required; no download is performed.')
const ts=deps.load('typescript'),compiler=deps.load('@vue/compiler-sfc'),vue=deps.load('vue'),{renderToString}=deps.load('@vue/server-renderer')
const fixture={project:null,requests:[],postOverride:null,directory:[]}
let nonce=0
const copy=value=>JSON.parse(JSON.stringify(value))
const envelope=data=>({data:copy(data),meta:{replayed:false,refresh_required:false}})
function request(method,url,body,config){
 fixture.requests.push({method,url,body:body===undefined?undefined:copy(body),config:config&&copy(config)})
 if(method==='GET'){
 if(url.endsWith('/members/directory'))return Promise.resolve(envelope(fixture.directory))
 if(url.endsWith('/invitations')||url.endsWith('/owner-transfers')||url.endsWith('/project-invitations'))return Promise.resolve(envelope([]))
 return Promise.resolve(envelope(fixture.project))
 }
 if(fixture.postOverride)return fixture.postOverride(method,url,body,config)
 if(url.endsWith('/invitations'))return Promise.resolve(envelope({id:'synthetic-invitation',project_id:fixture.project.id,user_id:body.assignment.user_id,version:1,assignment:body.assignment,status:'invited',expires_at:'2999-01-01T00:00:00Z',reason:body.reason}))
 fixture.project={...fixture.project,project_version:fixture.project.project_version+1}
 if(method==='PUT'&&url.endsWith('/assignment'))fixture.project.members=fixture.project.members.map(member=>member.user_id===body.assignment.user_id?{...body.assignment,status:'active'}:member)
 return Promise.resolve(envelope(fixture.project))
}
const requestMock={get:(url,config)=>request('GET',url,undefined,config),post:(url,body,config)=>request('POST',url,body,config),put:(url,body,config)=>request('PUT',url,body,config)}
function compile(filename,source,resolve){const item=new Module(filename);item.paths=Module._nodeModulePaths(deps.directory);item.require=resolve||deps.load;item._compile(ts.transpileModule(source??fs.readFileSync(filename,'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022,esModuleInterop:true}}).outputText,filename);return item.exports}
const api=compile(path.join(frontend,'src/api/lingdoc/collaboration.ts'),undefined,id=>id==='@/utils/request'?requestMock:deps.load(id))
const resourceScope=compile(path.join(__dirname,'resourceScope.ts'))
const state=compile(path.join(__dirname,'collaborationState.ts'),undefined,id=>id==='./resourceScope'?resourceScope:deps.load(id))
const source=path.join(__dirname,'ProjectCollaborationPanel.vue'),parsed=compiler.parse(fs.readFileSync(source,'utf8'))
assert.deepEqual(parsed.errors,[])
const script=compiler.compileScript(parsed.descriptor,{id:'g5-ui-test'})
const template=compiler.compileTemplate({source:parsed.descriptor.template.content,filename:source,id:'g5-ui-test',compilerOptions:{bindingMetadata:script.bindings},isTS:true})
assert.deepEqual(template.errors,[])
const panel=compile(source,script.content,id=>id==='@/api/lingdoc/collaboration'?api:id==='@/api/lingdoc/candidateAdoption'?{newIdempotencyKey:()=>`synthetic-key-${++nonce}`} : id==='./collaborationState'?state:deps.load(id)).default
panel.render=compile(source+'.template',template.code).render
function project(){return {id:'project-ui',name:'合成协作验收',status:'active',project_version:4,spec_revision:1,current_context_revision:0,spec:{},template_id:'template-demo',template_version:'1',members:[{user_id:'owner',role:'owner',governance_role:'owner',status:'active'},{user_id:'writer',role:'collaborator',governance_role:'member',function_roles:['author'],function_scopes:{author:['chapter-a','future:preserved']},status:'active'},{user_id:'removed-user',role:'collaborator',governance_role:'member',function_roles:['author'],function_scopes:{author:['old-authorized-scope']},status:'removed'}]}}
async function render(props,action){
 fixture.project=copy(props.project);fixture.requests=[];fixture.postOverride=null;fixture.directory=[{user_id:'owner',name:'真实目录 Owner',tenant_role:'owner'},{user_id:'writer',name:'真实目录 Writer',tenant_role:'contributor'},{user_id:'new-user',name:'真实目录 User',tenant_role:'viewer'},{user_id:'removed-user',name:'真实目录 Removed',tenant_role:'contributor'}]
 let controller;const events=[]
 const component={...panel,setup(input,context){controller=panel.setup(input,context);return action?Promise.resolve(action(controller)).then(()=>controller):controller}}
 const app=vue.createSSRApp(component,{chapters:[],assets:[],currentUserId:'owner',...props,onChanged:()=>events.push('changed'),onDirty:dirty=>events.push(['dirty',dirty]),onClose:()=>events.push('close')})
 const html=await renderToString(app);return {html,controller,events}
}
test('unknown member states never become current or automatically restorable',()=>{
 assert.match(state.memberStateLabel('tenant_future_state'),/待识别状态/)
 assert.deepEqual(state.lifecycleActions({user_id:'u',role:'collaborator',status:'tenant_future_state'}),[])
 assert.deepEqual(state.lifecycleActions({user_id:'u',role:'collaborator',status:'removed'}),[])
 assert.deepEqual(state.lifecycleActions({user_id:'u',role:'collaborator',status:'suspended_by_tenant'}),['restore_tenant','remove'])
})
test('a removed member receives a fresh invitation draft, without old roles or scopes',()=>{
 const draft=state.assignmentDraft(project().members[2],4)
 assert.deepEqual(draft.roles,[]);assert.deepEqual(draft.scopes,{})
 state.setFunctionRole(draft,'author',true)
 assert.deepEqual(state.serializeAssignment(draft).function_scopes.author,[])
 assert.equal('scopeModes' in draft,false)
})
test('existing unknown scopes survive an unrelated single-member edit and Vue proxy cloning',()=>{
 const member=vue.reactive(project().members[1]);const draft=state.assignmentDraft(member,4)
 draft.governance='admin'
 const body=state.serializeAssignment(draft)
 assert.deepEqual(body.function_scopes.author,['chapter-a','future:preserved'])
 draft.scopes.author.push('new-scope');assert.deepEqual(member.function_scopes.author,['chapter-a','future:preserved'])
 assert.deepEqual(state.unseenScopes(draft,'author',[{id:'chapter-a',label:'A'}]),['future:preserved','new-scope'])
})
test('scopes only offer actual current project resources and do not offer draft chapters',()=>{
 const p=project(),chapters=[{id:'a',project_id:p.id,title:'A'},{id:'foreign',project_id:'other',title:'B'}]
 const assets=[{id:'asset-a',project_id:p.id,knowledge_id:'knowledge-a',title:'Source'},{id:'foreign-asset',project_id:'other',knowledge_id:'bad',title:'Foreign'}]
 const actual=state.resourceOptions(p,chapters,assets).map(item=>item.id)
 assert(actual.includes('a'));assert(actual.includes('asset:asset-a'));assert(!actual.includes('foreign'));assert(!actual.includes('asset:foreign-asset'))
 assert(!actual.some(scope=>scope.startsWith('knowledge:')||scope.startsWith('template:')||scope.startsWith('delivery:')))
 assert(!state.resourceOptions({...p,status:'draft'},chapters,assets).some(item=>item.id==='a'))
})
test('invitation acceptance requires the actual intended user and an unexpired recognized version state',()=>{
 const future='2999-01-01T00:00:00Z'
 assert.equal(state.actionableInvitation('invited',future,'target','target'),true)
 assert.equal(state.actionableInvitation('invited',future,'other','target'),false)
 assert.equal(state.actionableInvitation('future_status',future,'target','target'),false)
 assert.equal(state.actionableInvitation('invited','invalid','target','target'),false)
})
test('an identical retry preserves its key, while a different project basis gets a new key',()=>{
 let keys=0;const first=state.collaborationAttempt(undefined,{expected_project_version:4},()=>String(++keys))
 assert.equal(state.collaborationAttempt(first,{expected_project_version:4},()=>String(++keys)),first)
 assert.notEqual(state.collaborationAttempt(first,{expected_project_version:5},()=>String(++keys)).key,first.key)
 const epoch=new state.CollaborationEpoch();epoch.switchContext('project-a/owner');const old=epoch.capture();epoch.switchContext('project-b/owner');assert.equal(epoch.current(old),false)
})
test('the real member UI keeps removed members out of assignment and restore actions',async()=>{
 const {html}=await render({project:project()})
 assert.match(html,/重新邀请/);assert.match(html,/编辑职责与范围/);assert.match(html,/Owner 变更需要双方交接/)
 assert(!html.includes('value="owner"'))
})
test('the actual drawer renders and preserves unknown scopes without any JSON input',async()=>{
 const {html,controller}=await render({project:project()},s=>{s.openAssignment(project().members[1]);s.editor.value.reason='独立复核'})
 assert.match(html,/现存范围（暂未识别）：future:preserved/)
 assert.match(html,/保存该成员配置/);assert(!html.includes('JSON'))
 assert.equal(controller.hasUnsavedChanges.value,true)
})
test('changing invite target preserves the previous draft but does not copy its grants to another user',async()=>{
 const {controller}=await render({project:project()},s=>{s.directory.value=copy(fixture.directory);s.openInvite();s.selectInvitee('new-user');state.setFunctionRole(s.editor.value,'author',true);s.editor.value.scopes.author=['chapter-a'];s.editor.value.reason='previous user';s.selectInvitee('removed-user')})
 assert.deepEqual(controller.editor.value.roles,[]);assert.deepEqual(controller.editor.value.scopes,{})
 assert(Object.values(controller.drafts).some(draft=>draft.userId==='new-user'&&draft.roles.includes('author')))
})
test('single-member save calls only the typed assignment PUT and never a batch members replacement',async()=>{
 const {events}=await render({project:project()},async s=>{s.openAssignment(project().members[1]);s.editor.value.reason='复核原范围';await s.saveAssignment()})
 const writes=fixture.requests.filter(call=>call.method!=='GET');assert.equal(writes.length,1)
 assert.equal(writes[0].method,'PUT');assert.equal(writes[0].url,'/api/v1/lingdoc/projects/project-ui/members/writer/assignment')
 assert.deepEqual(writes[0].body.assignment.function_scopes.author,['chapter-a','future:preserved']);assert.equal(writes[0].body.expected_project_version,4)
 assert(events.includes('changed'))
})
test('late mutation responses cannot overwrite another project or emit its refresh event',async()=>{
 const {controller,events}=await render({project:project()},async s=>{s.openAssignment(project().members[1]);s.editor.value.reason='old project';let resolve;fixture.postOverride=()=>new Promise(done=>{resolve=done});const pending=s.saveAssignment();s.epoch.switchContext('another-project/owner');s.localProject.value={...project(),id:'another-project'};resolve(envelope(project()));await pending})
 assert.equal(controller.localProject.value.id,'another-project');assert(!events.includes('changed'));assert.equal(controller.hasUnsavedChanges.value,true)
})
test('readonly or non-Owner accounts cannot expose Owner handoff controls',async()=>{
 const p=project();p.members.push({user_id:'tenant-admin',role:'collaborator',governance_role:'member',function_roles:['observer'],status:'active',tenant_role:'admin'})
 const result=await render({project:p,currentUserId:'tenant-admin'},s=>{s.section.value='owner';s.openHandoff()})
 assert(!result.html.includes('提出 Owner 交接</button>'));assert.equal(result.controller.drawer.value,null)
 const read=await render({project:project(),readonly:true},s=>{s.section.value='owner';s.openHandoff()});assert.equal(read.controller.drawer.value,null)
})
test('unknown server acknowledgements keep the actual edited draft instead of clearing it as success',async()=>{
 const {controller}=await render({project:project()},async s=>{s.openInvite();s.selectInvitee('new-user');s.editor.value.reason='explicit synthetic unknown response';fixture.postOverride=()=>Promise.resolve(envelope({status:'provider_queued'}));await s.saveAssignment()})
 assert.equal(controller.hasUnsavedChanges.value,true);assert.equal(controller.drawer.value,'assignment');assert.match(controller.notice.value,/待识别状态/)
})
test('Escape closes only the drawer and preserves its dirty input',async()=>{
 const {controller,events}=await render({project:project()},s=>{s.openAssignment(project().members[1]);s.editor.value.reason='preserve me';let stopped=false;s.drawerKeys({key:'Escape',preventDefault(){},stopPropagation(){stopped=true}});assert(stopped)})
 assert.equal(controller.drawer.value,null);assert.equal(controller.hasUnsavedChanges.value,true);assert(!events.includes('close'))
})
test('Owner reject uses real current-version input and the native cancelled response state',async()=>{
 const {controller}=await render({project:project()},async s=>{s.rejectionReasons.transfer='human rejection';s.beginReject({id:'transfer'});fixture.postOverride=()=>Promise.resolve(envelope({id:'transfer',status:'cancelled'}));await s.rejectHandoff({id:'transfer',status:'pending',to_user_id:'writer',expected_project_version:4})})
 const write=fixture.requests.find(call=>call.method==='POST');assert.equal(write.url,'/api/v1/lingdoc/projects/project-ui/owner-transfer/transfer/reject');assert.deepEqual(write.body,{expected_project_version:4,reason:'human rejection'});assert.match(controller.notice.value,/已取消/)
})
test('API wrappers encode identities, keep resend version and operation key, and expose only safe invitation-entry GET',async()=>{
 fixture.requests=[]
 const assignment={user_id:'u/name',role:'collaborator',governance_role:'member',function_roles:[],function_scopes:{}}
 await api.inviteProjectMember('p/name',{expected_project_version:9,assignment,reason:'resend',invitation_id:'invite',expected_invitation_version:3},'same-key')
 await api.updateMemberAssignment('p/name','u/name',{expected_project_version:9,assignment,reason:'same user only'},'edit-key')
 await api.listMyProjectInvitations()
 assert.equal(fixture.requests[0].url,'/api/v1/lingdoc/projects/p%2Fname/invitations');assert.equal(fixture.requests[0].body.expected_invitation_version,3);assert.equal(fixture.requests[0].config.headers['Idempotency-Key'],'same-key')
 assert.equal(fixture.requests[1].url,'/api/v1/lingdoc/projects/p%2Fname/members/u%2Fname/assignment');assert.equal(fixture.requests[2].method,'GET');assert.equal(fixture.requests[2].url,'/api/v1/lingdoc/project-invitations')
})

test('a stale assignment basis stays dirty and performs no member write until explicit review',async()=>{
 const {controller}=await render({project:project()},async s=>{s.openAssignment(project().members[1]);s.editor.value.reason='stale draft';s.localProject.value={...s.localProject.value,project_version:5};await s.saveAssignment()})
 assert.equal(fixture.requests.filter(call=>call.method!=='GET').length,0);assert.equal(controller.hasUnsavedChanges.value,true);assert.match(controller.error.value,/版本已变化/)
})
test('the real invitation handler refuses another recipient and expired records before HTTP',async()=>{
 await render({project:project()},async s=>{const invite={id:'invite-other',user_id:'writer',status:'invited',expires_at:'2999-01-01T00:00:00Z',version:3};await s.acceptInvitation(invite);await s.acceptInvitation({...invite,user_id:'owner',expires_at:'2000-01-01T00:00:00Z'})})
 assert.equal(fixture.requests.filter(call=>call.method!=='GET').length,0)
})

test('an Owner handoff draft stays with its original project and cannot become the next project request',async()=>{
 const {controller}=await render({project:project()},s=>{s.handoff.value.toUserId='writer';s.handoff.value.reviewed=true;s.localProject.value={...project(),id:'second-project'};s.epoch.switchContext('second-project/owner')})
 assert.equal(controller.handoff.value.toUserId,'');assert.equal(controller.handoff.value.reviewed,false);assert.equal(controller.hasUnsavedChanges.value,true)
 assert(Object.values(controller.handoffCache).some(draft=>draft.toUserId==='writer'&&draft.reviewed))
})

test('Owner acceptance is a transfer receipt; the UI rereads Project before adopting the new Owner role',async()=>{
 const p=project()
 const {controller}=await render({project:p,currentUserId:'writer'},async s=>{
 fixture.postOverride=()=>{fixture.project={...p,project_version:5,members:p.members.map(member=>member.user_id==='writer'?{...member,role:'owner',governance_role:'owner'}:member.user_id==='owner'?{...member,role:'collaborator',governance_role:'member'}:member)};return Promise.resolve(envelope({id:'transfer',project_id:p.id,from_user_id:'owner',to_user_id:'writer',status:'accepted',expected_project_version:4}))}
 await s.acceptHandoff({id:'transfer',project_id:p.id,to_user_id:'writer',status:'pending',expected_project_version:4})
 })
 assert.equal(controller.localProject.value.project_version,5);assert.equal(controller.access.value.owner,true);assert.match(controller.notice.value,/交接已接受/)
 assert(fixture.requests.some(call=>call.method==='GET'&&call.url==='/api/v1/lingdoc/projects/project-ui'))
 assert(fixture.requests.some(call=>call.method==='GET'&&call.url.endsWith('/members/directory')))
 const post=fixture.requests.find(call=>call.method==='POST');assert.deepEqual(post.body,{})
})

test('the actual member drawer displays empty scope as no object write grant and sends no invented whole-project scope',async()=>{
 const p=project();p.members[1].function_scopes={author:[]}
 const {html}=await render({project:p},async s=>{s.openAssignment(p.members[1]);s.editor.value.reason='保留无对象授权状态';await s.saveAssignment()})
 assert.match(html,/未分配对象范围（不授予章节或资料写权限）/)
 assert(!html.includes('全部对象'));assert(!html.includes('整个项目的对象'));assert(!html.includes('范围模式'))
 const write=fixture.requests.find(call=>call.method==='PUT');assert.deepEqual(write.body.assignment.function_scopes,{author:[]})
})
test('backend-only and unknown scope IDs remain readonly in the actual drawer and survive one-member save',async()=>{
 const p=project();p.members[1].function_scopes.author=['chapter-a','knowledge:actual-knowledge','template:actual-template','delivery:actual-project','future:preserved']
 const {html}=await render({project:p},async s=>{s.openAssignment(p.members[1]);s.editor.value.reason='仅调整治理身份，范围不变';s.editor.value.governance='admin';await s.saveAssignment();s.openAssignment(fixture.project.members[1])})
 assert.match(html,/后端管理，原样保留/)
 const write=fixture.requests.find(call=>call.method==='PUT');assert.deepEqual(write.body.assignment.function_scopes.author,p.members[1].function_scopes.author)
 const parsed=html.match(/<label class="checkbox retained-scope">[^]*?<\/label>/g)||[]
 assert(parsed.length>=4);assert(parsed.every(label=>label.includes('disabled')))
})
test('legacy collaborator identity never invents an Author role or an object scope during assignment editing',()=>{
 const draft=state.assignmentDraft({user_id:'legacy',role:'collaborator',status:'active'},4)
 assert.deepEqual(draft.roles,[]);assert.deepEqual(draft.scopes,{})
 assert.deepEqual(state.serializeAssignment(draft).function_roles,[])
})
