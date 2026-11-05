import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,readFile,mkdir,rename,rmdir} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {createWorkspaceService} from './workspace-service.mjs';
const sha=t=>createHash('sha256').update(t).digest('hex');
async function fixture(options={}) {
 const root=path.join(path.dirname(fileURLToPath(import.meta.url)),'artifacts','unit-tests');await mkdir(root,{recursive:true});const directory=await mkdtemp(path.join(root,'case-'));
 const text='四个社区表示愿意协助联系与招募。';
 const doc={text,source:{id:'source-1',asset_id:'asset-1',quoted_text_hash:sha(text),status:'available'},segments:[]};
 const assets=[{id:'asset-1',knowledge_id:'knowledge-1',title:'社区联系记录.txt',asset_revision:1}];
 const calls=[];let modelCalls=0;
 const fetchImpl=async(url,opts)=>{
  const route=new URL(url).pathname;const body=opts.body&&JSON.parse(opts.body);calls.push({route,body,key:opts.headers['Idempotency-Key']});let data;
  if(route.startsWith('/api/v1/chunks/'))data=[{id:'source-1',chunk_type:'text'}];
  else if(route.endsWith('/retrieval'))data=options.empty?[]:[doc.source];
  else if(route.endsWith('/policy'))data={model_use_allowed:true,version:1};
  else if(route.endsWith('/sessions'))data={id:'go-session-'+calls.length};
  else if(route.endsWith('/turns')){modelCalls++;if(options.beforeModel)await options.beforeModel();if(options.throwModel)throw Error('transport lost');data={id:'turn-'+modelCalls,status:'completed',run_mode:'real',model_id:'real-test-model',reply:'四个社区愿意协助联系与招募。',suggestions:{history_usage:{included_turns:modelCalls-1,excluded_turns:0,truncated_turns:0,reasons:{}},citations:[{source_id:options.badCitation?'foreign':'source-1',quote:text}]}};}
  else if(route.endsWith('/projects/project-1'))data={project_version:1,current_context_revision:1};
  else throw Error('Unexpected route '+route);
  if(route.endsWith('/turns')&&options.failedTurn)data={id:'failed-'+modelCalls,status:'failed',...options.failedTurn};
  return {ok:true,status:200,json:async()=>({data})};
 };
 const args={directory,goOrigin:'http://127.0.0.1:18000',origin:'http://127.0.0.1:18001',projectId:'project-1',sources:{listAssets:async()=>assets,readSource:async id=>{assert.equal(id,'source-1');return structuredClone(doc)}},fetchImpl,pdfFixture:null,imports:options.imports};
 const service=await createWorkspaceService(args);return {service,args,directory,calls,doc,get modelCalls(){return modelCalls}};
}
test('real QA adapter sends only project sources; persists history and idempotent response',async()=>{
 const f=await fixture();const before=await f.service.state();
 await f.service.act({kind:'send',key:'question-001',text:'几个社区愿意协助？'});
 const state=await f.service.state();assert.equal(state.messages.length,2);assert.equal(state.messages[1].citations[0].sourceId,'source-1');assert.ok(state.stateRevision>before.stateRevision);
 const model=f.calls.find(c=>c.route.endsWith('/turns'));assert.equal(model.body.document_selection,undefined);assert.deepEqual(model.body.source_ids,[]);assert.equal(model.body.source_excerpts[0].quoted_text,'四个社区表示愿意协助联系与招募。');assert.ok(model.key);
 await f.service.act({kind:'send',key:'question-001',text:'几个社区愿意协助？'});assert.equal(f.modelCalls,1);
 const restored=await createWorkspaceService(f.args);assert.equal((await restored.state()).messages.length,2);
 await assert.rejects(()=>restored.act({kind:'send',key:'question-001',text:'changed'}),/操作编号/);
});
test('new sessions have no old context or dialogue; returning restores own state',async()=>{
 const f=await fixture();const id=(await f.service.state()).currentSessionId;
 await f.service.act({kind:'addContext',key:'context-001',sourceId:'source-1'});
 await f.service.act({kind:'send',key:'question-001',text:'社区'});
 await f.service.act({kind:'newSession',key:'new-chat-001'});
 let state=await f.service.state();assert.notEqual(state.currentSessionId,id);assert.equal(state.contextSources.length,0);assert.equal(state.messages.length,0);
 await f.service.act({kind:'selectSession',key:'select-chat-001',sessionId:id});state=await f.service.state();assert.equal(state.contextSources.length,1);assert.equal(state.messages.length,2);
});
test('removing context excludes that material from future search and calls',async()=>{
 const f=await fixture();await f.service.act({kind:'addContext',key:'context-001',sourceId:'source-1'});await f.service.act({kind:'removeContext',key:'context-002',sourceId:'source-1'});
 await f.service.act({kind:'send',key:'question-001',text:'社区'});assert.equal(f.modelCalls,0);const s=await f.service.state();assert.equal(s.messages.at(-1).kind,'retrieval_empty');assert.match(s.contextNote,/旧聊天/);
});
test('busy state is observable and context/session mutations reject immediately',async()=>{
 let release,started;const active=new Promise(r=>started=r),gate=new Promise(r=>release=r);
 const f=await fixture({beforeModel:async()=>{started();await gate}});const sending=f.service.act({kind:'send',key:'question-001',text:'社区'});await active;
 assert.equal((await f.service.state()).busy,true);await assert.rejects(()=>f.service.act({kind:'newSession',key:'new-chat-001'}),/尚未完成/);
 assert.equal((await f.service.openSource('source-1')).payload.op,'source.openText');release();await sending;assert.equal((await f.service.state()).busy,false);
});
test('unknown model outcome is persisted without automatic resubmit',async()=>{
 const f=await fixture({throwModel:true});await assert.rejects(()=>f.service.act({kind:'send',key:'question-001',text:'社区'}),/结果尚未确定/);
 const restored=await createWorkspaceService(f.args);await restored.act({kind:'send',key:'question-001',text:'社区'});assert.equal(f.modelCalls,1);
 await assert.rejects(()=>restored.act({kind:'send',key:'question-002',text:'继续'}),/尚未核对/);
 const disk=JSON.parse(await readFile(path.join(f.directory,'native-workspace.json'),'utf8'));assert.ok(disk.operations['question-001'].model.key);assert.equal(disk.sessions[0].uncertain,true);
});
test('invented model source is not displayed as an answer',async()=>{
 const f=await fixture({badCitation:true});await assert.rejects(()=>f.service.act({kind:'send',key:'question-001',text:'社区'}),/出处/);const s=await f.service.state();assert.equal(s.messages.length,1);assert.ok(s.error);assert.equal(s.busy,false);
});
test('old citation versions are rejected without changing reader or pinned context',async()=>{
 const f=await fixture();const before=await f.service.state();
 await assert.rejects(()=>f.service.openSource('source-1',1,'0'),/版本/);
 assert.deepEqual(await f.service.state(),before);
 await assert.rejects(()=>f.service.act({kind:'addContext',key:'old-version-001',sourceId:'source-1',version:'0'}),/版本/);
 assert.deepEqual((await f.service.state()).contextSources,[]);
 assert.equal((await f.service.openSource('source-1',1,'1')).payload.version,'1');
});
test('changed original text or source hash cannot silently replace an old citation',async()=>{
 const f=await fixture();const before=await f.service.state();f.doc.text='现在只有两个社区。';f.doc.source.quoted_text_hash=sha(f.doc.text);
 await assert.rejects(()=>f.service.openSource('source-1',1,'1'),/原文已变化/);
 assert.deepEqual(await f.service.state(),before);
 await assert.rejects(()=>f.service.act({kind:'addContext',key:'changed-source-001',sourceId:'source-1',version:'1'}),/原文已变化/);
 assert.deepEqual((await f.service.state()).contextSources,[]);
});
test('pinned source fingerprint survives reopening and refuses changed material before model use',async()=>{
 const f=await fixture();await f.service.act({kind:'addContext',key:'pin-version-001',sourceId:'source-1',version:'1'});
 f.doc.text='资料在关闭后被改动。';f.doc.source.quoted_text_hash=sha(f.doc.text);
 const restored=await createWorkspaceService(f.args);
 await assert.rejects(()=>restored.act({kind:'send',key:'question-changed-001',text:'继续'}),/固定资料内容已变化/);
 assert.equal(f.modelCalls,0);assert.equal((await restored.state()).contextSources[0].version,'1');
});
test('stale client session cannot send into a different active conversation',async()=>{
 const f=await fixture();const original=(await f.service.state()).currentSessionId;
 await f.service.act({kind:'newSession',key:'switch-new-001',expectedSessionId:original});
 const before=await f.service.state();
 await assert.rejects(()=>f.service.act({kind:'send',key:'stale-send-001',expectedSessionId:original,text:'旧窗口的问题'}),/会话已切换/);
 assert.deepEqual(await f.service.state(),before);assert.equal(f.modelCalls,0);
 const disk=JSON.parse(await readFile(path.join(f.directory,'native-workspace.json'),'utf8'));assert.equal(disk.operations['stale-send-001'],undefined);
 await f.service.act({kind:'selectSession',key:'switch-back-001',expectedSessionId:before.currentSessionId,sessionId:original});
 assert.equal((await f.service.state()).currentSessionId,original);
});


test('imports refresh catalog atomically, preserve originals and expose project state',async()=>{
 const records=[];let release,started;const gate=new Promise(r=>release=r),active=new Promise(r=>started=r);
 const text='四个社区表示愿意协助联系与招募。';
 const record={id:'import-1',name:'访谈.pdf',folder:'参考文献',status:'ready',sourceId:'source-1',assetId:'asset-1',version:'1',searchable:true,indexedTextSha256:sha(text)};
 const imports={list:async()=>structuredClone(records),importFile:async()=>{started();await gate;records.push(record);return record;},resolve:async()=>({record,payload:{op:'source.open',title:'访谈.pdf',pages:['original-page']},pageTexts:[{page:1,text}]})};
 const f=await fixture({imports});const importing=f.service.importFile({name:'访谈.pdf',key:'import-001',projectId:'project-1'});await active;
 assert.equal((await f.service.state()).importBusy,true);assert.equal((await f.service.state()).sources[0].title,'社区联系记录.txt');
 await assert.rejects(()=>f.service.act({kind:'send',key:'during-import-001',text:'社区'}),/正在导入/);assert.equal(f.modelCalls,0);
 release();await importing;const state=await f.service.state();assert.equal(state.projectId,'project-1');assert.equal(state.importBusy,false);assert.equal(state.imports.length,1);assert.equal(state.sources[0].title,'访谈.pdf');
 const opened=await f.service.openSource('source-1',1,'1');assert.deepEqual(opened.payload.pages,['original-page']);
 await f.service.act({kind:'send',key:'after-import-001',text:'社区'});assert.equal((await f.service.state()).messages.at(-1).citations[0].page,1);
 await assert.rejects(()=>f.service.importFile({projectId:'another'}),/项目已切换/);
});

test('image-only PDF remains readable but cannot become model context',async()=>{
 const record={id:'scan-1',sourceId:'scan-1',name:'扫描.pdf',status:'readable_only',version:'1',searchable:false};
 const imports={list:async()=>[record],resolve:async()=>({record,payload:{op:'source.open',title:record.name,pages:['scan-page']}})};
 const f=await fixture({imports});assert.equal((await f.service.state()).sources.find(s=>s.id==='scan-1').searchable,false);
 assert.equal((await f.service.openSource('scan-1')).payload.pages[0],'scan-page');
 await assert.rejects(()=>f.service.act({kind:'addContext',key:'scan-context-001',sourceId:'scan-1'}),/仅支持阅读/);assert.equal(f.modelCalls,0);
});

test('failed imports retain visible errors without replacing the live source catalog',async()=>{
 const records=[];const imports={list:async()=>records,importFile:async()=>{records.push({id:'failed-1',name:'损坏.pdf',status:'failed',error:'解析失败'});throw Error('解析失败')}};
 const f=await fixture({imports});await assert.rejects(()=>f.service.importFile({name:'损坏.pdf',key:'import-failed-001'}),/解析失败/);
 const state=await f.service.state();assert.equal(state.importBusy,false);assert.equal(state.imports[0].status,'failed');assert.equal(state.sources.length,1);assert.match(state.importError,/解析失败/);
});


test('oversized retrieved material remains readable and reports context limit without model call',async()=>{
 const f=await fixture();f.doc.text='长'.repeat(12001);f.doc.source.quoted_text_hash=sha(f.doc.text);
 const restored=await createWorkspaceService(f.args);
 await restored.act({kind:'send',key:'long-document-001',text:'研究资料讲什么？'});
 const answer=(await restored.state()).messages.at(-1);assert.equal(answer.kind,'context_over_budget');assert.match(answer.text,/已找到资料/);assert.match(answer.text,/12000/);assert.doesNotMatch(answer.text,/没有找到/);assert.equal(f.modelCalls,0);
 assert.equal((await restored.openSource('source-1')).payload.text.length,12001);
});


test('catalog quarantines only locally proven imported assets, never arbitrary authorization failures',async()=>{
 const f=await fixture();const denied=()=>Object.assign(Error('unavailable'),{status:403,code:'source_access_denied'});
 const args={...f.args,sources:{...f.args.sources,readSource:async()=>{throw denied()}}};
 const record={id:'import-known',name:'Original.md',sourceId:'import-known',status:'readable_only',searchable:false,version:'1',unavailableAssetId:'asset-1'};
 const service=await createWorkspaceService({...args,imports:{list:async()=>[record]}});
 const state=await service.state();assert.equal(state.sources.length,1);assert.equal(state.sources[0].id,'import-known');assert.equal(state.catalogWarnings.length,1);
 const textHash=sha(f.doc.text),originalFetch=f.args.fetchImpl;
 const legacy=await createWorkspaceService({...args,imports:{list:async()=>[{id:'old-import',status:'failed',extractedTextSha256:textHash}]},fetchImpl:async(url,opts)=>url.endsWith('/knowledge/knowledge-1')?{ok:true,status:200,json:async()=>({data:{metadata:{local_operation_key:'import-old-import',local_content_sha256:textHash}}})}:originalFetch(url,opts)});
 assert.equal((await legacy.state()).catalogWarnings.length,1);
 await assert.rejects(()=>createWorkspaceService({...args,imports:{list:async()=>[]},fetchImpl:async(url,opts)=>url.endsWith('/knowledge/knowledge-1')?{ok:true,status:200,json:async()=>({data:{metadata:{}}})}:originalFetch(url,opts)}),/unavailable/);
 await assert.rejects(()=>createWorkspaceService({...args,imports:{list:async()=>[record]},sources:{...f.args.sources,readSource:async()=>{throw Object.assign(Error('account denied'),{status:403,code:'forbidden'})}}}),/account denied/);
});


test('follow-up without retrieval reuses exact prior source and keeps the same Go conversation',async()=>{
 const options={};const f=await fixture(options);
 await f.service.act({kind:'send',key:'followup-first-001',text:'社区能提供哪些帮助？'});
 options.empty=true;
 await f.service.act({kind:'send',key:'followup-second-001',text:'把刚才的内容整理成两点'});
 const turns=f.calls.filter(c=>c.route.endsWith('/turns'));
 assert.equal(turns.length,2);assert.equal(turns[0].route,turns[1].route);
 assert.deepEqual(turns[1].body.source_excerpts,turns[0].body.source_excerpts);
 const state=await f.service.state();assert.equal(state.messages.length,4);
 assert.equal(state.messages.at(-1).historyUsage.included_turns,1);
 assert.equal(state.messages.at(-1).usedSources[0].textHash,sha(f.doc.text));
 // Removing and explicitly adding back changes only future material scope.
 await f.service.act({kind:'removeContext',key:'followup-remove-001',sourceId:'source-1'});
 await f.service.act({kind:'send',key:'followup-excluded-001',text:'继续'});
 assert.equal(f.modelCalls,2);assert.equal((await f.service.state()).messages.at(-1).kind,'retrieval_empty');
 await f.service.act({kind:'addContext',key:'followup-readd-001',sourceId:'source-1',version:'1'});
 await f.service.act({kind:'send',key:'followup-readded-001',text:'继续核对资料'});
 assert.equal(f.modelCalls,3);assert.equal((await f.service.state()).messages[1].citations[0].sourceId,'source-1');
});

test('follow-up continuity refuses changed original after service restart before model use',async()=>{
 const options={};const f=await fixture(options);
 await f.service.act({kind:'send',key:'history-original-001',text:'社区情况'});
 f.doc.text='该资料原文后来被更改。';f.doc.source.quoted_text_hash=sha(f.doc.text);options.empty=true;
 const restarted=await createWorkspaceService(f.args);
 await assert.rejects(()=>restarted.act({kind:'send',key:'history-changed-001',text:'展开刚才的第二点'}),/上轮使用的资料内容已变化/);
 assert.equal(f.modelCalls,1);
 assert.equal((await restarted.state()).messages[1].usedSources[0].textHash,sha('四个社区表示愿意协助联系与招募。'));
});

test('a pinned source that disappears can still be removed without reading the lost original',async()=>{
 const f=await fixture();await f.service.act({kind:'addContext',key:'missing-pin-001',sourceId:'source-1',version:'1'});
 let reads=0;
 const restarted=await createWorkspaceService({...f.args,sources:{...f.args.sources,listAssets:async()=>[],readSource:async()=>{reads++;throw Error('source gone')}}});
 assert.equal((await restarted.state()).contextSources.length,1);
 await restarted.act({kind:'removeContext',key:'missing-remove-001',sourceId:'source-1'});
 assert.equal((await restarted.state()).contextSources.length,0);assert.equal(reads,0);
 const disk=JSON.parse(await readFile(path.join(f.directory,'native-workspace.json'),'utf8'));
 assert.deepEqual(disk.sessions[0].excludedAssets,['asset-1']);
});


test('provider failures explain manual next steps safely and do not block a new question',async()=>{
 const options={failedTurn:{failure_code:'provider_failure',error:'secret upstream payload must never reach UI'}};
 const f=await fixture(options);
 await assert.rejects(()=>f.service.act({kind:'send',key:'provider-fail-001',text:'社区'}),/AI 服务本轮未能完成回答/);
 const state=await f.service.state();assert.match(state.error,/不会自动重发/);assert.doesNotMatch(state.error,/secret upstream/);assert.equal(state.busy,false);
 assert.equal(state.messages.length,1);
 await f.service.act({kind:'send',key:'provider-fail-001',text:'社区'});assert.equal(f.modelCalls,1);
 delete options.failedTurn;
 await f.service.act({kind:'send',key:'provider-new-002',text:'改问社区能做什么？'});
 assert.equal(f.modelCalls,2);assert.equal((await f.service.state()).messages.at(-1).role,'assistant');
});

test('known indeterminate outcome remains blocked despite a failed turn record',async()=>{
 const f=await fixture({failedTurn:{failure_code:'outcome_unknown'}});
 await assert.rejects(()=>f.service.act({kind:'send',key:'outcome-fail-001',text:'社区'}),/结果尚未确定/);
 await assert.rejects(()=>f.service.act({kind:'send',key:'outcome-new-002',text:'再问一次'}),/尚未核对/);
 assert.equal(f.modelCalls,1);
});


test('narrower retrieval preserves prior permitted sources; explicit removal still excludes history material',async()=>{
 const f=await fixture();
 const doc2={text:'访谈将在下周开展。',source:{id:'source-2',asset_id:'asset-2',quoted_text_hash:sha('访谈将在下周开展。'),status:'available'},segments:[]};
 const assets=[...(await f.args.sources.listAssets()),{id:'asset-2',knowledge_id:'knowledge-2',title:'访谈安排.txt',asset_revision:1}];
 const turns=[];let retrievalIds=['source-1','source-2'];
 const service=await createWorkspaceService({...f.args,sources:{...f.args.sources,listAssets:async()=>assets,readSource:async id=>id==='source-2'?structuredClone(doc2):f.args.sources.readSource(id)},fetchImpl:async(url,opts)=>{
  const route=new URL(url).pathname;
  const response=data=>({ok:true,status:200,json:async()=>({data})});
  if(route.endsWith('/chunks/knowledge-2'))return response([{id:'source-2',chunk_type:'text'}]);
  if(route.endsWith('/retrieval'))return response(retrievalIds.map(id=>({id})));
  if(route.endsWith('/turns')){
   const body=JSON.parse(opts.body);turns.push({route,body});const excerpt=body.source_excerpts[0];
   return response({id:'multi-turn-'+turns.length,status:'completed',run_mode:'real',model_id:'synthetic-unit',reply:'仅测试上下文结构。',suggestions:{citations:[{source_id:excerpt.source_id,quote:excerpt.quoted_text}]}});
  }
  return f.args.fetchImpl(url,opts);
 }});
 await service.act({kind:'send',key:'narrow-first-001',text:'概括资料'});
 assert.deepEqual(turns[0].body.source_excerpts.map(s=>s.source_id),['source-1','source-2']);
 await service.act({kind:'addContext',key:'narrow-pin-001',sourceId:'source-1',version:'1'});
 retrievalIds=['source-1'];
 await service.act({kind:'send',key:'narrow-second-001',text:'展开刚才第二点'});
 assert.deepEqual(turns[1].body.source_excerpts.map(s=>s.source_id),['source-1','source-2']);
 assert.equal(turns[1].route,turns[0].route);
 await service.act({kind:'removeContext',key:'narrow-remove-001',sourceId:'source-1'});
 // Even if a stale retrieval result contains the removed source, it cannot re-enter.
 retrievalIds=['source-1','source-2'];
 await service.act({kind:'send',key:'narrow-third-001',text:'只根据剩余资料回答'});
 assert.deepEqual(turns[2].body.source_excerpts.map(s=>s.source_id),['source-2']);
 assert.deepEqual((await service.state()).messages.at(-1).usedSources.map(s=>s.sourceId),['source-2']);
 assert.equal(turns[2].route,turns[0].route);
});

test('an oversized pinned material is never silently omitted in favor of retrieved sources',async()=>{
 const f=await fixture();const text='长'.repeat(12001);
 const long={text,source:{id:'source-long',asset_id:'asset-long',quoted_text_hash:sha(text)},segments:[]};
 const response=data=>({ok:true,status:200,json:async()=>({data})});
 const service=await createWorkspaceService({...f.args,sources:{...f.args.sources,listAssets:async()=>[...(await f.args.sources.listAssets()),{id:'asset-long',knowledge_id:'knowledge-long',title:'完整研究资料.txt',asset_revision:1}],readSource:async id=>id==='source-long'?structuredClone(long):f.args.sources.readSource(id)},fetchImpl:async(url,opts)=>{
  if(new URL(url).pathname.endsWith('/chunks/knowledge-long'))return response([{id:'source-long',chunk_type:'text'}]);
  return f.args.fetchImpl(url,opts);
 }});
 await service.act({kind:'addContext',key:'budget-pin-long',sourceId:'source-long',version:'1'});
 await assert.rejects(()=>service.act({kind:'send',key:'budget-ask-long',text:'结合固定资料回答'}),/固定资料.*12000/);
 assert.equal(f.modelCalls,0);const state=await service.state();assert.equal(state.contextSources.length,1);assert.equal(state.messages.length,1);assert.equal(state.busy,false);
});

test('upstream HTTP and transport details never leak into UI errors or persisted operations',async()=>{
 for(const mode of ['http','transport']){
  const f=await fixture();const secret='upstream-secret-token-not-for-ui';
  const service=await createWorkspaceService({...f.args,fetchImpl:async(url,opts)=>{
   if(new URL(url).pathname.endsWith('/turns')){
    if(mode==='transport')throw Error(secret);
    return {ok:false,status:502,json:async()=>({error:{code:'provider_failure',message:secret}})};
   }
   return f.args.fetchImpl(url,opts);
  }});
  await assert.rejects(()=>service.act({kind:'send',key:'safe-error-'+mode,text:'社区'}),error=>!error.message.includes(secret));
  assert.doesNotMatch((await service.state()).error,new RegExp(secret));
  assert.doesNotMatch(await readFile(path.join(f.directory,'native-workspace.json'),'utf8'),new RegExp(secret));
 }
});

test('same-tick sends cannot queue a second model call behind an active request',async()=>{
 let release,started;const gate=new Promise(r=>release=r),active=new Promise(r=>started=r);
 const f=await fixture({beforeModel:async()=>{started();await gate}});
 const first=f.service.act({kind:'send',key:'concurrent-first',text:'社区'});
 const second=f.service.act({kind:'send',key:'concurrent-second',text:'意外重复发送'}).then(()=>({accepted:true}),error=>({accepted:false,error}));
 await active;release();await first;
 const result=await second;assert.equal(result.accepted,false);assert.match(result.error.message,/尚未完成|稍候/);assert.equal(f.modelCalls,1);
 await f.service.act({kind:'send',key:'after-completed',text:'合法连续追问'});assert.equal(f.modelCalls,2);
});

test('a failed retained import cannot fall back to an ordinary searchable asset',async()=>{
 const f=await fixture();await f.service.act({kind:'addContext',key:'import-before-fail',sourceId:'source-1',version:'1'});
 const record={id:'import-1',assetId:'asset-1',sourceId:'source-1',name:'资料.pdf',status:'failed',searchable:false,version:'1',error:'保留原件校验失败'};
 const service=await createWorkspaceService({...f.args,imports:{list:async()=>[record],resolve:async()=>{throw Error('failed original')}}});
 const state=await service.state();assert.equal(state.sources.length,0);assert.equal(state.catalogWarnings.length,1);
 await assert.rejects(()=>service.openSource('source-1',1,'1'),/不属于当前项目或已不可读取/);
 await service.act({kind:'removeContext',key:'remove-failed-import',sourceId:'source-1'});
 await service.act({kind:'send',key:'no-failed-import-use',text:'回答资料中的问题'});
 assert.equal(f.modelCalls,0);assert.equal((await service.state()).messages.at(-1).kind,'retrieval_empty');
});

test('a same-tick import is rejected before an already accepted send can start',async()=>{
 let imported=false;const f=await fixture({imports:{list:async()=>[],importFile:async()=>{imported=true;return {}}}});
 const sending=f.service.act({kind:'send',key:'send-before-import',text:'社区'});
 await assert.rejects(()=>f.service.importFile({name:'new.txt',key:'same-tick-import'}),/尚未完成/);
 await sending;assert.equal(imported,false);assert.equal(f.modelCalls,1);assert.equal((await f.service.state()).importBusy,false);
});

test('same-process failed reimport immediately removes the stale indexed asset from QA',async()=>{
 const record={id:'import-live',assetId:'asset-1',sourceId:'source-1',name:'资料.pdf',status:'ready',searchable:true,version:'1',indexedTextSha256:sha('四个社区表示愿意协助联系与招募。')};
 const imports={list:async()=>structuredClone([record]),importFile:async()=>{record.status='failed';record.searchable=false;throw Error('保留副本校验失败；不会覆盖原件。')}};
 const f=await fixture({imports});await f.service.act({kind:'addContext',key:'live-import-pin',sourceId:'source-1',version:'1'});
 await assert.rejects(()=>f.service.importFile({name:'资料.pdf',key:'live-invalid-import'}),/保留副本校验失败/);
 const state=await f.service.state();assert.equal(state.sources.length,0);assert.equal(state.imports[0].status,'failed');assert.equal(state.importBusy,false);assert.equal(state.catalogWarnings.length,1);
 await assert.rejects(()=>f.service.act({kind:'send',key:'live-invalid-question',text:'结合固定资料回答'}),/已不可读取|已失效/);assert.equal(f.modelCalls,0);
 await f.service.act({kind:'removeContext',key:'live-invalid-remove',sourceId:'source-1'});assert.equal((await f.service.state()).contextSources.length,0);
});

test('initial import state persistence failure releases the busy lock',async()=>{
 let imported=false;const f=await fixture({imports:{list:async()=>[],importFile:async()=>{imported=true;return {}}}});
 const file=path.join(f.directory,'native-workspace.json'),backup=path.join(f.directory,'native-workspace.before-test.json');
 // Only this test's generated temporary file is moved; restore it even on assertion failure.
 await rename(file,backup);await mkdir(file);
 try{
  await assert.rejects(()=>f.service.importFile({name:'x.txt',key:'save-failure-import'}));
  assert.equal((await f.service.state()).importBusy,false);assert.equal(imported,false);
 }finally{await rmdir(file);await rename(backup,file)}
 await f.service.act({kind:'send',key:'after-import-save-failure',text:'社区'});assert.equal(f.modelCalls,1);
});

test('import status refresh failure cannot replace the original import error',async()=>{
 let failed=false;const f=await fixture({imports:{list:async()=>{if(failed)throw Error('secondary refresh failure');return []},importFile:async()=>{failed=true;throw Error('original import failure')}}});
 await assert.rejects(()=>f.service.importFile({name:'x.txt',key:'refresh-double-failure'}),/original import failure/);
 const state=await f.service.state();assert.equal(state.importError,'original import failure');assert.equal(state.importBusy,false);
});
