import test from 'node:test'
import assert from 'node:assert/strict'
import {mountPanel,project,response,clone,flush,text} from './r26ComponentRuntimeFixture.js'

function fixture(readonly=false) {
 let run={id:'run',version:2,status:'completed',run_mode:'mock',correlation_id:'demo',basis:{context_revision:4},rounds:[{number:1,query:'synthetic query',status:'completed',feedback:''}],results:[],usage:{searches_used:1,search_limit:8,results_used:0,result_limit:160,model_calls_used:0,model_call_limit:0}}
 const task={id:'task',goal:'Test plan',status:'completed',basis:{context_revision:4},queries:['q1','q2'],exclusions:[],latest_run_id:'run'}
 const f=mountPanel('ResearchPanel',{props:{project:project(),readonly},transport(call){
  if(call.method==='GET'&&call.url.endsWith('/research-tasks'))return response([clone(task)])
  if(call.method==='GET'&&call.url.endsWith('/research-runs'))return response([clone(run)])
  if(call.method==='GET'&&call.url.endsWith('/research-runs/run'))return response(clone(run))
  if(call.method==='POST'&&call.url.endsWith('/research-runs/run/feedback')){assert.equal(call.body.expected_version,2);assert.equal(call.body.round_number,1);assert.equal(call.body.feedback,'补充反例');assert.ok(call.config.headers['Idempotency-Key']);run.version=3;run.rounds[0].feedback=call.body.feedback;run.rounds[0].feedback_by='u';run.rounds[0].feedback_at='2026-10-28T00:00:00Z';return response({run_id:'run',run_version:3,round_number:1})}
 }})
 return f
}
test('研究者可以保存并读回某轮查询反馈',async()=>{
 const f=fixture();try{
  await flush();await f.click('查看结果');await f.edit(f.field('本轮反馈'),'补充反例')
  const button=f.find(n=>n.type==='button'&&text(n).trim()==='保存本轮反馈','feedback button')
  let form=button.parent;while(form&&form.type!=='form')form=form.parent
  assert.ok(form);await form.props.onSubmit({preventDefault(){},stopPropagation(){}});await flush()
  assert.match(f.ui(),/反馈：补充反例/)
  assert.equal(f.calls.filter(c=>c.method==='POST'&&c.url.endsWith('/feedback')).length,1)
 }finally{f.close()}
})
test('只读成员可以看已有结果但看不到写反馈入口',async()=>{
 const f=fixture(true);try{await flush();await f.click('查看结果');assert.equal(f.field('本轮反馈'),undefined);assert.equal(f.calls.filter(c=>c.method==='POST').length,0)}finally{f.close()}
})
