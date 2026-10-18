import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
const require = createRequire(import.meta.url)
const __filename = fileURLToPath(import.meta.url)
const __dirname = require('node:path').dirname(__filename)

// Actual Vue script/template and typed API execution. HTTP is an explicit
// synthetic fixture; no browser, live backend or model call is performed.
const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),Module=require('node:module')
const frontend=path.resolve(__dirname,'../../..'),load=Module.createRequire(path.join(frontend,'package.json')),ts=load('typescript'),compiler=load('@vue/compiler-sfc'),vue=load('vue'),{renderToString}=load('@vue/server-renderer')
const clone=v=>JSON.parse(JSON.stringify(v)),envelope=data=>({data:clone(data),meta:{replayed:false,refresh_required:false}})
let fixture,nonce=0
function request(method,url,body,config){
 fixture.requests.push({method,url,body:body&&clone(body),config:config&&clone(config)})
 if(method==='GET'){
  if(fixture.getOverride){const result=fixture.getOverride(url);if(result)return result}
  if(url.endsWith('/versions'))return Promise.resolve(envelope({items:fixture.versions}))
  if(url.endsWith('/comments'))return Promise.resolve(envelope(fixture.comments))
  const status=fixture.review.chapter_version_id!==fixture.props.chapter.current_version_id||fixture.review.context_revision!==fixture.props.project.current_context_revision?'STALE':fixture.review.status
  return Promise.resolve(envelope({...fixture.review,status}))
 }
 if(fixture.postOverride)return fixture.postOverride(url,body,config)
 if(url.endsWith('/review')){fixture.review={...fixture.review,version:fixture.review.version+1,status:body.action==='return'?'DRAFT':'IN_REVIEW',reason:body.reason,chapter_version_id:body.chapter_version_id,context_revision:body.expected_context_revision};return Promise.resolve(envelope(fixture.review))}
 let item=body.id?fixture.comments.find(item=>item.id===body.id):undefined
 if(item){item={...item,version:item.version+1,resolved:true};fixture.comments=fixture.comments.map(old=>old.id===item.id?item:old)}else{item={id:'synthetic-comment-'+(++nonce),chapter_id:'c1',chapter_version_id:body.chapter_version_id,context_revision:body.expected_context_revision,version:1,actor_id:'reviewer',body:body.body,selection:body.selection,resolved:false,created_at:'2026-10-14T01:00:00Z',updated_at:'2026-10-14T01:00:00Z'};fixture.comments.push(item)}
 return Promise.resolve(envelope(item))
}
function compile(filename,source,resolve){const m=new Module(filename);m.paths=Module._nodeModulePaths(frontend);m.require=resolve||load;m._compile(ts.transpileModule(source??fs.readFileSync(filename,'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022,esModuleInterop:true}}).outputText,filename);return m.exports}
const api=compile(path.join(frontend,'src/api/lingdoc/chapterReview.ts'),undefined,id=>id==='@/utils/request'?{get:(url,c)=>request('GET',url,undefined,c),post:(url,b,c)=>request('POST',url,b,c)}:load(id))
const source=path.join(__dirname,'ChapterReviewPanel.vue'),parsed=compiler.parse(fs.readFileSync(source,'utf8'));assert.deepEqual(parsed.errors,[])
const script=compiler.compileScript(parsed.descriptor,{id:'g7-review-test'}),template=compiler.compileTemplate({source:parsed.descriptor.template.content,filename:source,id:'g7-review-test',compilerOptions:{bindingMetadata:script.bindings},isTS:true});assert.deepEqual(template.errors,[])
const panel=compile(source,script.content,id=>id==='@/api/lingdoc/chapterReview'?api:id==='@/api/lingdoc/candidateAdoption'?{newIdempotencyKey:()=>`synthetic-key-${++nonce}`} :load(id)).default;panel.render=compile(source+'.template',template.code).render
function setupProps(){return {project:{id:'p1',name:'合成评审',status:'active',project_version:4,spec_revision:1,current_context_revision:1,members:[{user_id:'owner',role:'owner',status:'active'},{user_id:'reviewer',role:'collaborator',governance_role:'member',function_roles:['reviewer'],function_scopes:{reviewer:['c1']},status:'active'}]},chapter:{id:'c1',project_id:'p1',title:'实际章节',current_version_id:'v1',body_markdown:'当前正文目标片段'},currentUserId:'reviewer',readonly:false,formalCurrent:true}}
function version(id,body_markdown){return {id,project_id:'p1',chapter_id:'c1',parent_version_id:null,body_markdown,source_ids:[],review_items:[],spec_revision:1,confirmation_valid:false,created_at:'2026-10-14T00:00:00Z'}}
async function render(overrides,action){
 const props=vue.reactive({...setupProps(),...overrides})
 fixture={props,requests:[],review:{chapter_id:'c1',chapter_version_id:'v1',context_revision:1,spec_revision:1,version:2,status:'IN_REVIEW',actor_id:'reviewer',reason:'请补充反例与来源',updated_at:'2026-10-14T00:00:00Z'},versions:[version('v1','当前正文目标片段'),version('old','旧版正文仅供对照')],comments:[],getOverride:null,postOverride:null}
 let controller;const events=[]
 const component={...panel,setup(input,context){controller=panel.setup(input,context);return Promise.resolve(controller.load()).then(()=>action?.(controller,props)).then(()=>controller)}}
 const html=await renderToString(vue.createSSRApp(component,{...props,onChanged:()=>events.push('changed'),onLocate:s=>events.push(['locate',s]),onDirty:d=>events.push(['dirty',d])}))
 return {controller,props,html,events,requests:clone(fixture.requests)}
}

test('the typed version reader uses the existing encoded GET and actual items envelope',async()=>{
 await render({},async()=>{const result=await api.listReviewChapterVersions('p/name','c/name');assert.equal(result.data.items[0].id,'v1')})
 assert(fixture.requests.some(r=>r.method==='GET'&&r.url==='/api/v1/lingdoc/projects/p%2Fname/chapters/c%2Fname/versions'))
 assert.equal(fixture.requests.filter(r=>r.method==='POST').length,0)
})
test('the actual UI displays the stored return reason and both fixed formal version bodies',async()=>{
 const {html}=await render({},s=>{s.comparisonId.value='old'})
 assert.match(html,/请补充反例与来源/);assert.match(html,/当前正文目标片段/);assert.match(html,/旧版正文仅供对照/);assert(!html.includes('恢复到工作副本'))
})
test('Reviewer can return the explicitly inspected fixed formal version while the working copy differs',async()=>{
 const {controller}=await render({formalCurrent:false},async s=>{s.reason.value='按正式版本核对后退回';s.formalAcknowledged.value=true;await s.review('return')})
 const write=fixture.requests.find(r=>r.method==='POST');assert.equal(write.body.chapter_version_id,'v1');assert.equal(write.body.expected_review_version,2);assert.equal(controller.state.value.status,'DRAFT')
})
test('an Author still cannot submit review from an uncommitted working-copy difference',async()=>{
 const p=setupProps();p.currentUserId='owner'
 await render({...p,formalCurrent:false},async s=>{s.reason.value='not submitted';s.formalAcknowledged.value=true;await s.review('submit')})
 assert.equal(fixture.requests.filter(r=>r.method==='POST').length,0)
})
test('same-chapter version and Context change preserve input and require explicit re-association before any write',async()=>{
 await render({selection:{start_utf16:4,end_utf16:8,quote_markdown:'目标片段'}},async(s,p)=>{
 s.body.value='保留原批注';s.reason.value='保留原退回理由';assert.equal(s.draft.value.selection.quote_markdown,'目标片段')
 fixture.versions.unshift(version('v2','新版本不同正文'));p.chapter.current_version_id='v2';p.project.current_context_revision=2;p.project.project_version=5
 await s.load();assert.equal(s.body.value,'保留原批注');assert.equal(s.reason.value,'保留原退回理由');assert.equal(s.draftStale.value,true)
 s.formalAcknowledged.value=true;await s.comment();await s.review('return');assert.equal(fixture.requests.filter(r=>r.method==='POST').length,0)
 await s.rebaseDraft();assert.equal(s.draftStale.value,false);assert.equal(s.draft.value.selection,undefined);assert.equal(s.formalAcknowledged.value,false)
 await s.comment();const write=fixture.requests.find(r=>r.method==='POST');assert.equal(write.body.chapter_version_id,'v2');assert.equal(write.body.expected_context_revision,2);assert.equal(write.body.selection,undefined)
 })
})
test('old-context GET results never overwrite the freshly read same-chapter state',async()=>{
 await render({},async(s,p)=>{
 let finish;fixture.getOverride=url=>{if(url.endsWith('/review')){fixture.getOverride=null;return new Promise(resolve=>finish=resolve)}}
 const pending=s.load();p.project.current_context_revision=2;await s.load();assert.equal(s.state.value.status,'STALE')
 finish(envelope({...fixture.review,status:'IN_REVIEW'}));await pending;assert.equal(s.state.value.status,'STALE')
 })
})
test('a late mutation does not clear a draft or emit completion after its formal basis changed',async()=>{
 const {events,controller}=await render({},async(s,p)=>{
 s.reason.value='old pinned reason';s.formalAcknowledged.value=true;let finish;fixture.postOverride=()=>new Promise(resolve=>finish=resolve)
 const pending=s.review('return');p.project.current_context_revision=2;await s.load();finish(envelope({...fixture.review,status:'DRAFT',version:3}));await pending
 })
 assert.equal(controller.reason.value,'old pinned reason');assert.equal(controller.draftStale.value,true);assert(!events.includes('changed'))
})
test('unknown or mismatched acknowledgements retain the actual typed user input',async()=>{
 const {controller}=await render({},async s=>{s.body.value='不要丢失';fixture.postOverride=()=>Promise.resolve(envelope({status:'provider_queued'}));await s.comment()})
 assert.equal(controller.body.value,'不要丢失');assert.match(controller.notice.value,/回执尚未确认/)
})
test('identical failed submissions keep their exact idempotency key and retained reason',async()=>{
 await render({},async s=>{s.reason.value='重试同一次退回';s.formalAcknowledged.value=true;fixture.postOverride=()=>Promise.reject(new Error('synthetic lost response'));await s.review('return');await s.review('return');assert.equal(s.reason.value,'重试同一次退回')})
 const calls=fixture.requests.filter(r=>r.method==='POST');assert.equal(calls.length,2);assert.equal(calls[0].config.headers['Idempotency-Key'],calls[1].config.headers['Idempotency-Key'])
})
test('historical comments keep their own body/version and never guess a current-editor location',async()=>{
 const {events,html}=await render({},s=>{const old={id:'old-note',chapter_version_id:'old',actor_id:'reviewer',body:'历史问题',selection:{start_utf16:0,end_utf16:2,quote_markdown:'旧版'},resolved:false};s.comments.value=[old];s.locateComment(old);s.showCommentVersion(old)})
 assert(!events.some(event=>Array.isArray(event)&&event[0]==='locate'));assert.match(html,/历史正式版本/);assert.match(html,/旧版正文仅供对照/);assert.match(html,/不在当前工作正文猜测位置/)
})
test('current comment locating requires exact quote equality with the actually read formal body',async()=>{
 const {events}=await render({},s=>{s.locateComment({chapter_version_id:'v1',selection:{start_utf16:4,end_utf16:8,quote_markdown:'目标片段'}});s.locateComment({chapter_version_id:'v1',selection:{start_utf16:4,end_utf16:8,quote_markdown:'旧的正文'}})})
 assert.equal(events.filter(event=>Array.isArray(event)&&event[0]==='locate').length,1)
})
test('an unavailable current formal version cannot be replaced with the props working body or used for mutations',async()=>{
 await render({},async s=>{fixture.versions=[version('old','旧版')];await s.load();assert.equal(s.ready.value,false);s.body.value='draft';await s.comment()})
 assert.equal(fixture.requests.filter(r=>r.method==='POST').length,0)
})
test('resolving a historical comment preserves a separately pending current-version draft and its selection',async()=>{
 const {controller}=await render({selection:{start_utf16:4,end_utf16:8,quote_markdown:'目标片段'}},async s=>{
 const old={id:'old-note',chapter_id:'c1',chapter_version_id:'old',context_revision:1,version:1,actor_id:'reviewer',body:'旧问题',resolved:false};fixture.comments=[old];s.body.value='另一个未提交批注';await s.comment(old)
 const write=fixture.requests.find(r=>r.method==='POST');assert.equal(write.body.chapter_version_id,'old');assert.equal(write.body.selection,undefined)
 })
 assert.equal(controller.body.value,'另一个未提交批注');assert.equal(controller.draft.value.selection.quote_markdown,'目标片段')
})
test('a scope without its actual function role cannot expose author/reviewer actions',async()=>{
 const p=setupProps();p.project.members[1].function_roles=[]
 const {controller,html}=await render(p)
 assert.equal(controller.canAuthor.value,false);assert.equal(controller.canReview.value,false);assert(!html.includes('带理由退回当前正式版本'))
})
test('formal range validation refuses out-of-bounds and split-surrogate selections',async()=>{
 await render({},s=>{const v=version('emoji','甲😀乙');assert.equal(s.selectionFits({start_utf16:1,end_utf16:3,quote_markdown:'😀'},v),true);assert.equal(s.selectionFits({start_utf16:1,end_utf16:2,quote_markdown:v.body_markdown.slice(1,2)},v),false);assert.equal(s.selectionFits({start_utf16:0,end_utf16:100,quote_markdown:v.body_markdown},v),false)})
})
test('a still-matching parent selection is never silently carried across a newer formal basis',async()=>{
 await render({selection:{start_utf16:4,end_utf16:8,quote_markdown:'目标片段'}},async(s,p)=>{p.project.current_context_revision=2;await s.load();s.body.value='本次新批注';assert.equal(s.draft.value.selection,undefined)})
})