import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {createWorkflowModel} from './workflow-model.mjs';
const hash=t=>createHash('sha256').update(t).digest('hex');
function fixture({sources=true,badCitation=false,failTurn=false}={}) {
  const calls=[];let turnFailed=false;
  const candidate={id:'wf-test',target:{documentId:1,revision:2,text:'选区原文',anchor:{off:0},focus:{off:4}},instruction:'只限定样本范围',context:{sources:sources?[{sourceId:'s1',text:'资料原文',sha256:hash('资料原文'),quotedTextHash:'source-hash'}]:[],comment:{text:'老师意见'}},modelOperations:{}};
  const model=createWorkflowModel({goOrigin:'http://127.0.0.1:1',origin:'http://127.0.0.1:2',projectId:'p1',sources:{readSource:async()=>({source:{id:'s1'},text:'资料原文'})},fetchImpl:async(url,options)=>{
    const route=new URL(url).pathname,body=options.body?JSON.parse(options.body):null;calls.push({route,body,key:options.headers['Idempotency-Key']});
    if(route.endsWith('/turns')) {
      if(failTurn&&!turnFailed){turnFailed=true;throw Error('synthetic network loss');}
      return Response.json({id:'turn1',status:'completed',run_mode:'real',model_id:'local-real',reply:'候选新文',suggestions:{citations:sources?[{source_id:badCitation?'foreign':'s1',quote:'资料'}]:[]}});
    }
    if(route.endsWith('/sessions'))return Response.json({id:'session1'});
    if(route.endsWith('/policy'))return Response.json({model_use_allowed:true,version:1});
    if(route.endsWith('/sources/s1'))return Response.json({id:'s1',asset_id:'a1',quoted_text_hash:'source-hash'});
    return Response.json({project_version:2,current_context_revision:3});
  }});
  return {model,candidate,calls};
}
test('Go payload contains exactly selected context and isolated session',async()=>{const f=fixture();const r=await f.model.generate(f.candidate,async()=>{});assert.equal(r.modelReceipt.runMode,'real');const turn=f.calls.find(c=>c.route.endsWith('/turns'));assert.equal(turn.body.document_selection.text,'选区原文');assert.deepEqual(turn.body.source_ids,[]);assert.equal(turn.body.source_excerpts[0].quoted_text,'资料原文');assert.equal(turn.body.source_excerpts[0].selection_end,4);assert.match(turn.body.user_input,/老师意见/);assert.equal(turn.body.sensitive_context_confirmed,true);assert.equal(f.calls.filter(c=>c.route.endsWith('/sessions')).length,1);});
test('no sources invokes no source or policy endpoints',async()=>{const f=fixture({sources:false});await f.model.generate(f.candidate,async()=>{});assert.equal(f.calls.filter(c=>c.route.includes('/sources/')||c.route.includes('/policy')).length,0);});
test('unknown model reply replays durable same turn body and key',async()=>{const f=fixture({failTurn:true});await assert.rejects(()=>f.model.generate(f.candidate,async()=>{}),/network loss/);await f.model.generate(f.candidate,async()=>{});const turns=f.calls.filter(c=>c.route.endsWith('/turns'));assert.equal(turns.length,2);assert.deepEqual(turns[0],turns[1]);assert.equal(f.calls.filter(c=>c.route.endsWith('/sessions')).length,1);});
test('rejects citation outside provided sources',async()=>{const f=fixture({badCitation:true});await assert.rejects(()=>f.model.generate(f.candidate,async()=>{}),/引用/);});
test('remote success with lost local checkpoint replays exact operation key',async()=>{const f=fixture({sources:false});let durable=structuredClone(f.candidate);let failed=false;const checkpoint=async()=>{if(f.candidate.modelOperations.turn?.done&&!failed){failed=true;throw Error('checkpoint disk failure')}durable=structuredClone(f.candidate);};await assert.rejects(()=>f.model.generate(f.candidate,checkpoint),/checkpoint disk failure/);f.candidate=durable;await f.model.generate(f.candidate,async()=>{});const turns=f.calls.filter(c=>c.route.endsWith('/turns'));assert.equal(turns.length,2);assert.deepEqual(turns[0],turns[1]);assert.equal(f.calls.filter(c=>c.route.endsWith('/sessions')).length,1);});
