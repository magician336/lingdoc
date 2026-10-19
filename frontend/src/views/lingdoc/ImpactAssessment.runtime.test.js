import test from 'node:test'
import assert from 'node:assert/strict'
import {mountPanel,project,response,clone,deferred,flush,text} from './r26ComponentRuntimeFixture.js'
const change=(status='draft')=>({id:'c1',project_id:'p',created_by:'u',reason:'Synthetic draft',status,base_context_revision:4,base_spec_revision:3,
 fields:[{key:'topic',old_value:'Saved topic',new_value:'New topic'}],impacts:[{chapter_id:'ch1'}]})
const assessment={change_set_id:'c1',project_id:'p',context_revision:4,input_hash:'r26-assessment-hash',ruleset_hash:'synthetic-rules',impacts:[]}
function fixture(options={}){
 let server=change(options.assessed?'assessed':'draft')
 const pending=options.pendingAssess?deferred():null,preview=options.pendingPreview?deferred():null
 const f=mountPanel('ImpactOpsPanel',{props:{project:project()},transport(call){
  if(call.method==='GET'&&call.url.endsWith('/change-sets'))return response(call.url.includes('/projects/p/')?[clone(server)]:[])
  if(call.method==='POST'&&call.url.endsWith('/c1/assess')){server=change('assessed');return pending?pending.promise:response(clone(server))}
  if(call.method==='GET'&&call.url.endsWith('/c1/impact-assessment'))return preview?preview.promise:response(assessment)
 }})
 return Object.assign(f,{pending,preview,setAssessed(){server=change('assessed')}})
}
test('R26 first successful assess click renders actual impact preview without a second click',async()=>{
 const f=fixture();try{
  await flush();await f.click('评估当前影响')
  assert.match(f.ui(),/影响预览 · 基线 4/,'first assess must show preview from current assessed row, not return early on captured draft state')
  assert.match(f.ui(),/r26-assessment-hash/)
  assert.equal(f.calls.filter(c=>c.url.endsWith('/c1/assess')).length,1)
  assert.equal(f.calls.filter(c=>c.url.endsWith('/c1/impact-assessment')).length,1)
 }finally{f.close()}
})
test('R26 project switch while assess is pending cannot fetch or display old project preview',async()=>{
 const f=fixture({pendingAssess:true});try{
  await flush();const click=f.click('评估当前影响');await flush()
  assert.equal(f.calls.filter(c=>c.url.endsWith('/c1/assess')).length,1)
  f.holder.project=project('p2');await flush();f.pending.resolve(response(change('assessed')));await click;await flush()
  assert.match(f.ui(),/Synthetic p2/);assert.doesNotMatch(f.ui(),/影响预览|r26-assessment-hash/)
  assert.equal(f.calls.filter(c=>c.url.endsWith('/c1/impact-assessment')).length,0)
 }finally{f.close()}
})
test('R26 project switch during existing impact GET rejects its late response',async()=>{
 const f=fixture({pendingPreview:true,assessed:true});try{
  await flush();const click=f.click('查看影响依据');await flush()
  assert.equal(f.calls.filter(c=>c.url.endsWith('/c1/impact-assessment')).length,1)
  f.holder.project=project('p2');await flush();f.preview.resolve(response(assessment));await click;await flush()
  assert.match(f.ui(),/Synthetic p2/);assert.doesNotMatch(f.ui(),/影响预览|r26-assessment-hash/)
 }finally{f.close()}
})

// --- N1: impact preview must expose the assessment response itself, not a
// re-derivation from the manually registered dependency list.
const richAssessment={
 change_set_id:'c1',project_id:'p',context_revision:4,template_copy_version:1,ruleset_hash:'synthetic-rules',
 input_hash:'r26-rich-assessment-hash',semantic_hash:'r26-rich-semantic-hash',assessed_at:'2026-02-02T00:00:00Z',
 objects:[
  {ref:{kind:'ChapterVersion',id:'ch-required-only',version:'7'},state:'CURRENT',source_ids:['src-1'],confirmed:true,context_revision:4},
  {ref:{kind:'ResearchClaim',id:'claim-required-only',version:'2'},state:'STALE',source_ids:[],confirmed:false,context_revision:3}],
 edges:[
  {id:'edge-eval-1',project_id:'p',source:{kind:'ResearchClaim',id:'claim-src',version:'1'},target:{kind:'ChapterVersion',id:'ch-target',version:'9'},
   relation:'supports',action:'REVALIDATE_EVIDENCE',origin:'rule',reason:'评估快照依据边',status:'confirmed',confidence:1,actor_id:'u',version:1,created_at:'2026-02-02T00:00:00Z',updated_at:'2026-02-02T00:00:00Z'}],
 impacts:[
  {target:{kind:'ChapterVersion',id:'ch-definite',version:'3'},action:'REVALIDATE_EVIDENCE',certainty:'definite',
   causes:[{edge_id:'edge-eval-1',source:{kind:'ResearchClaim',id:'claim-cause-src',version:'5'},reason:'确定影响原因'}]},
  {target:{kind:'ChapterVersion',id:'ch-possible',version:'4'},action:'NO_ACTION',certainty:'possible',
   causes:[{edge_id:'edge-eval-2',source:{kind:'SourceAnchor',id:'anchor-possible-src',version:'6'},reason:'可能影响原因'}]}]}
const manualEdge={id:'edge-manual-1',project_id:'p',source:{kind:'EvidenceCard',id:'card-manual-src',version:'1'},
 target:{kind:'ChapterVersion',id:'ch-manual-target',version:'1'},relation:'supports',action:'REVALIDATE_EVIDENCE',
 origin:'user',reason:'人工登记边原因',status:'suggested',confidence:0.5,actor_id:'u',version:1,
 created_at:'2026-02-02T00:00:00Z',updated_at:'2026-02-02T00:00:00Z'}
function richFixture(){
 const server=change('assessed')
 const f=mountPanel('ImpactOpsPanel',{props:{project:project()},transport(call){
  if(call.method==='GET'&&call.url.endsWith('/change-sets'))return response(call.url.includes('/projects/p/')?[clone(server)]:[])
  if(call.method==='GET'&&call.url.endsWith('/c1/impact-assessment'))return response(clone(richAssessment))
  if(call.method==='GET'&&call.url.endsWith('/dependencies'))return response([clone(manualEdge)])
 }})
 return f
}
async function richPreview(){ const f=richFixture();await flush();await f.click('查看影响依据');await flush();return f }
test('N1 impact preview renders the required object set with contract identity, state and confirmation',async()=>{
 const f=await richPreview();try{
  const ui=f.ui()
  assert.match(ui,/本次必须处理的对象/,'the required object set must have its own block')
  // These two object ids never occur in any impact target, so a match cannot
  // come from the impact list coincidentally repeating the same id/version.
  assert.match(ui,/章节版本\s*·\s*ChapterVersion\/ch-required-only\s*@\s*7/,'contract identity of a required object must be visible')
  assert.match(ui,/当前版本/,'the object state must be shown in Chinese')
  assert.match(ui,/已确认/,'a confirmed object must say so')
  assert.match(ui,/研究主张\s*·\s*ResearchClaim\/claim-required-only\s*@\s*2/,'every required object must be listed')
  assert.match(ui,/未确认/,'an unconfirmed object must say so')
  assert.match(ui,/基线 3/,'the object baseline revision must be visible')
 }finally{f.close()}
})
test('N1 impact preview renders the cause source object with its contract identity',async()=>{
 const f=await richPreview();try{
  const ui=f.ui()
  assert.match(ui,/确定影响原因\s*·\s*来源\s*研究主张\s*·\s*ResearchClaim\/claim-cause-src\s*@\s*5\s*·\s*边\s*edge-eval-1/,'the cause source object must be rendered, not only the edge id')
  assert.match(ui,/可能影响原因\s*·\s*来源\s*资料锚点\s*·\s*SourceAnchor\/anchor-possible-src\s*@\s*6\s*·\s*边\s*edge-eval-2/)
 }finally{f.close()}
})
test('N1 definite and possible impacts are rendered in two separate lists',async()=>{
 const f=await richPreview();try{
  const uls=f.nodes().filter(node=>node.type==='ul')
  const definite=uls.find(node=>text(node).includes('ch-definite'))
  const possible=uls.find(node=>text(node).includes('ch-possible'))
  assert.ok(definite,'a list holding the definite impacts must exist')
  assert.ok(possible,'a list holding the possible impacts must exist')
  assert.notEqual(definite,possible,'the two certainty groups must not share one list element')
  assert.ok(!definite.children.includes(possible)&&!possible.children.includes(definite),'the two certainty groups must not be nested in each other')
  assert.doesNotMatch(text(definite),/ch-possible/,'the definite list must hold only definite impacts')
  assert.doesNotMatch(text(possible),/ch-definite/,'the possible list must hold only possible impacts')
  assert.match(text(definite),/确定影响/)
  assert.match(text(possible),/可能影响/)
 }finally{f.close()}
})
test('N1 impact preview shows the assessment edge snapshot, not the registered dependency list',async()=>{
 const f=await richPreview();try{
  const ui=f.ui()
  assert.match(ui,/本次评估依据的依赖边/,'the assessment edge snapshot needs its own block')
  assert.match(ui,/来自本次评估响应的快照/,'the block must state that it comes from the assessment response')
  assert.match(ui,/研究主张\s*·\s*ResearchClaim\/claim-src\s*→\s*章节版本\s*·\s*ChapterVersion\/ch-target/,'the assessment edge endpoints must use contract identity')
  assert.match(ui,/评估快照依据边/,'the assessment edge reason must be visible')
  assert.doesNotMatch(ui,/人工登记边原因|edge-manual-1|card-manual-src/,'the registered dependency list must not stand in for the assessment evidence')
 }finally{f.close()}
})
