import test from 'node:test'
import assert from 'node:assert/strict'
import {mountPanel,project,response,clone,flush,deferred,text} from './r26ComponentRuntimeFixture.js'
const AI='R26_SYNTHETIC_PRIVATE_AI'
const MANUAL='R26 unrelated manual question must survive'
function taskTurn(){return {id:'turn',session_id:'session',project_id:'p',version:2,agent:'task_assistant',created_by:'u',status:'completed',run_mode:'mock',reply:AI,user_input:'synthetic input',
 context:{project_id:'p',project_version:7,spec_revision:3,context_revision:4,source_refs:[{source_id:'source',asset_id:'asset',asset_revision:1,quoted_text_hash:'synthetic-hash',model_use_allowed:true}],card_refs:[],output_constraints:[],authorization_state:'allowed'},
 suggestions:{task_card:{goal:AI+' goal',queries:[AI+' q1',AI+' q2'],scope:AI+' scope',question_type:'synthetic',exclusions:[],expected_output:AI+' output'},change_draft:{expected_context_revision:4,reason:AI+' reason',fields:{topic:AI+' new topic'},affected_chapter_ids:['ch1']}}}}
function fixture(status='active'){
 let restricted=false;const queuedTurnReads=[]
 const source=taskTurn(),p=project('p',status)
 const f=mountPanel('G2ResearchPanel',{props:{project:p},transport(call){
  if(call.url.endsWith('/agents/task_assistant/sessions'))return response([{id:'session',project_id:'p',version:1,agent:'task_assistant',created_by:'u',created_at:'2026-10-18T00:00:00Z'}])
  if(call.url.endsWith('/agent-sessions/session/turns')){
   if(queuedTurnReads.length)return queuedTurnReads.shift().promise
   const out=clone(source);if(restricted){out.status='restricted';out.reply='';out.user_input='';delete out.suggestions}
   return response([out])
  }
  if(call.url.endsWith('/chapters'))return response([{id:'ch1',project_id:'p',title:'Synthetic chapter'}])
 }})
 return Object.assign(f,{allowedTurn:()=>clone(source),restrictedTurn(){const out=clone(source);out.status='restricted';out.reply='';out.user_input='';delete out.suggestions;return out},deferNextTurnRead(){const pending=deferred();queuedTurnReads.push(pending);return pending},revoke(){restricted=true},async open(){await flush();await f.click('助手与动作');await f.select(f.find(n=>n.type==='select'&&n.props['aria-label']==='独立会话','session select'),'session');assert.match(f.ui(),new RegExp(AI))},async question(){await f.edit(f.field('本轮问题'),MANUAL)},assertNoWrites(){assert.equal(f.calls.filter(c=>c.method!=='GET').length,0)}})
}
test('R26 restricted receipt clears copied AI task values and preserves unrelated manual question',async()=>{
 const f=fixture();try{
  await f.open();await f.question();await f.click('放入可编辑任务草稿');assert.match(f.field('要弄清什么').value,new RegExp(AI))
  await f.click('助手与动作');f.revoke();await f.click('读取会话回执')
  assert.equal(f.field('本轮问题').value,MANUAL)
  await f.click('1 调研')
  assert.equal(f.field('要弄清什么').value,'','revoked source-derived task goal must be removed')
  assert.equal(f.field('至少两条查询').value,'','revoked source-derived queries must be removed')
  f.assertNoWrites()
 }finally{f.close()}
})
test('R26 restricted receipt clears edited AI change-set JSON without erasing manual question',async()=>{
 const f=fixture();try{
  await f.open();await f.question();await f.click('编辑为变更草稿提案')
  const input=f.field('拟议字段变更（可编辑 fields 和 reason）');assert.match(input.value,new RegExp(AI))
  await f.edit(input,input.value.replace(AI+' new topic',AI+' manually amended derivative'))
  f.revoke();await f.click('读取会话回执')
  const after=f.field('拟议字段变更（可编辑 fields 和 reason）')
  assert.ok(!after||after.value==='','edited AI derivative remains tainted until source verification and must be cleared')
  assert.equal(f.field('本轮问题').value,MANUAL);assert.doesNotMatch(f.ui(),new RegExp(AI));f.assertNoWrites()
 }finally{f.close()}
})
test('R26 authoritative restricted spec re-read clears copied candidate but preserves independent human input',async()=>{
 const f=fixture('draft');try{
  await f.open();await f.question();await f.click('用于研究条件候选')
  assert.equal(f.field('候选值').value,AI)
  await f.edit(f.field('候选值'),AI+' edited candidate');f.revoke();await f.click('读取当前条件与会话')
  const candidate=f.field('候选值');assert.ok(!candidate||candidate.value==='','restricted source must not remain copied in spec candidate editor')
  assert.equal(f.field('本轮问题').value,MANUAL);f.assertNoWrites()
 }finally{f.close()}
})
test('R26 manual-only task editor survives a restricted receipt and ordinary refresh',async()=>{
 const f=fixture();try{
  await f.open();await f.click('1 调研');await f.edit(f.field('要弄清什么'),'Independent human task');await f.edit(f.field('至少两条查询'),'Human query1\nHuman query2')
  await f.click('助手与动作');f.revoke();await f.click('读取会话回执');await f.click('重新读取');await f.click('1 调研')
  assert.equal(f.field('要弄清什么').value,'Independent human task');assert.equal(f.field('至少两条查询').value,'Human query1\nHuman query2');f.assertNoWrites()
 }finally{f.close()}
})


test('R26 source-bound confirmation closes after restricted receipt and queued submit cannot execute',async()=>{
 const f=fixture();try{
  await f.open();await f.question();await f.click('编辑为变更草稿提案')
  const pending=f.deferNextTurnRead(),before=f.calls.filter(c=>c.url.endsWith('/agent-sessions/session/turns')).length
  const reading=f.click('读取会话回执');await flush()
  assert.equal(f.calls.filter(c=>c.url.endsWith('/agent-sessions/session/turns')).length,before+1,'receipt read started before modal opened')
  const preview=f.find(n=>n.type==='button'&&text(n).trim()==='预览并保存提案','action submit')
  let form=preview.parent;while(form&&form.type!=='form')form=form.parent
  assert.ok(form&&typeof form.props.onSubmit==='function');await form.props.onSubmit({preventDefault(){},stopPropagation(){}});await flush()
  const dialog=f.find(n=>n.type==='dialog'&&n.open,'source-bound confirmation open'),confirmForm=dialog.children.find(n=>n.type==='form');assert.ok(confirmForm)
  await f.edit(f.field('本次决定原因'),'Human synthetic confirmation')
  const checked=dialog.children.flatMap(function walk(n){return [n,...(n.children||[]).flatMap(walk)]}).find(n=>n.tagName==='INPUT'&&n.props.type==='checkbox');assert.ok(checked);checked.checked=true;checked.dispatchEvent({type:'change',target:checked});await flush()
  const queuedSubmit=confirmForm.props.onSubmit;f.revoke();pending.resolve(response([f.restrictedTurn()]));await reading;await flush()
  assert.equal(dialog.open,false,'restricted source closes derived confirmation');assert.equal(f.field('本次决定原因'),undefined)
  assert.equal(f.nodes().filter(n=>n.type==='button'&&text(n).trim()==='明确确认').length,0)
  await queuedSubmit({preventDefault(){},stopPropagation(){}});await flush();f.assertNoWrites();assert.equal(f.field('本轮问题').value,MANUAL)
 }finally{f.close()}
})

test('R26 same-session newer restricted receipt wins over older allowed response and cannot re-enable copy',async()=>{
 const f=fixture();try{
  await f.open();await f.question();await f.click('放入可编辑任务草稿');await f.click('助手与动作')
  const before=f.calls.filter(c=>c.url.endsWith('/agent-sessions/session/turns')).length
  const older=f.deferNextTurnRead(),readOlder=f.click('读取会话回执');await flush()
  const newer=f.deferNextTurnRead(),readNewer=f.click('读取会话回执');await flush()
  assert.equal(f.calls.filter(c=>c.url.endsWith('/agent-sessions/session/turns')).length,before+2)
  f.revoke();newer.resolve(response([f.restrictedTurn()]));await readNewer;assert.doesNotMatch(f.ui(),new RegExp(AI))
  older.resolve(response([f.allowedTurn()]));await readOlder;await flush()
  assert.doesNotMatch(f.ui(),new RegExp(AI),'late allowed must not restore source text')
  assert.equal(f.nodes().filter(n=>n.type==='button'&&['放入可编辑任务草稿','编辑为变更草稿提案'].includes(text(n).trim())).length,0)
  assert.equal(f.field('本轮问题').value,MANUAL);await f.click('1 调研');assert.equal(f.field('要弄清什么').value,'');f.assertNoWrites()
 }finally{f.close()}
})

test('R26 starting another manual task detaches old AI source before revocation',async()=>{
 const f=fixture();try{
  await f.open();await f.click('放入可编辑任务草稿');assert.match(f.field('要弄清什么').value,new RegExp(AI))
  await f.click('新建另一任务');await f.edit(f.field('要弄清什么'),'Fresh human task after reset');await f.edit(f.field('至少两条查询'),'Fresh human query1\nFresh human query2')
  await f.click('助手与动作');f.revoke();await f.click('读取会话回执');await f.click('重新读取');await f.click('1 调研')
  assert.equal(f.field('要弄清什么').value,'Fresh human task after reset');assert.equal(f.field('至少两条查询').value,'Fresh human query1\nFresh human query2');f.assertNoWrites()
 }finally{f.close()}
})
