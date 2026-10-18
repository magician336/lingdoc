import test from 'node:test'
import assert from 'node:assert/strict'
import {mountPanel,project,response,clone,deferred,flush} from './r26ComponentRuntimeFixture.js'
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
